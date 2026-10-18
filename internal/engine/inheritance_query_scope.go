package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Closure is physical-identity deduplication within one SQL occurrence. A foreign
// session's temporary branch is not admitted, even through a persistent ancestor.
func inheritanceQueryClosure(catalog CatalogRead, root *relationDescriptor, token ids.SessionToken, visit func(*relationDescriptor) error) error {
	if err := visit(root); err != nil {
		return err
	}
	children := inheritanceChildren(catalog, root.address)
	if len(children) == 0 {
		return nil
	}
	seen := map[ids.ObjectAddress]bool{root.address: true}
	var walk func([]*relationDescriptor) error
	walk = func(children []*relationDescriptor) error {
		for _, child := range children {
			if seen[child.address] {
				continue
			}
			seen[child.address] = true
			ns, ok := catalog.Namespace(child.namespace)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			if ns.temporary != (ids.SessionToken{}) && ns.temporary != token {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
			if err := walk(inheritanceChildren(catalog, child.address)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(children)
}

// Called under Engine.mu. Stored views expose their named source's scan mode,
// not a flattened set of leaves or the mode of an unrelated SQL occurrence.
func inheritanceQueryLeaves(catalog CatalogRead, roots []queryLeaf, token ids.SessionToken, visit func(queryLeaf) error) error {
	var active []ids.ObjectAddress
	var walk func(queryLeaf) error
	walk = func(leaf queryLeaf) error {
		d, ok := catalog.Relation(leaf.ref.Address)
		if !ok || leaf.ref.Epoch != catalog.Epoch() {
			return ids.ErrCatalogReplaced
		}
		leaf.relation = d
		if d.relationKind == viewRelation {
			for _, address := range active {
				if address == d.address {
					return nil
				}
			}
			active = append(active, d.address)
			defer func() { active = active[:len(active)-1] }()
			for i, child := range viewOf(d).sources {
				if child.analysisOnly {
					continue
				}
				ref, err := storedViewUseReference(catalog, d, child.ref)
				if err != nil {
					return err
				}
				if err := walk(queryLeaf{ref: ref, only: child.only, mark: viewLeafRowMark(viewOf(d), i, leaf.mark)}); err != nil {
					return err
				}
			}
			return nil
		}
		if d.relationKind != tableRelation || leaf.only {
			return visit(leaf)
		}
		return inheritanceQueryClosure(catalog, d, token, func(physical *relationDescriptor) error {
			child := leaf
			child.ref.Address, child.relation = physical.address, physical
			return visit(child)
		})
	}
	for _, leaf := range roots {
		if !leaf.analysisOnly {
			if err := walk(leaf); err != nil {
				return err
			}
		}
	}
	return nil
}

// AccessShare on the named parent does not freeze CREATE INHERITS. Reconcile
// growth after every wait, then freeze one immutable topology for this statement.
// Previously admitted identities must survive; a same-name replacement is never
// a substitute. No manager wait is performed under Engine.mu.
func (s *StatementScope) acquireInheritanceSources(grants map[ids.ObjectRef]lock.Mode) error {
	e := s.op.session.Engine()
	roots := s.querySources
	if s.materializedRefresh {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		d, ok := catalog.Relation(s.lockedRef.Address)
		if !ok || d.view == nil {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		roots = append([]queryLeaf(nil), roots...)
		for _, leaf := range viewOf(d).sources {
			if leaf.analysisOnly {
				continue
			}
			ref, err := storedViewUseReference(catalog, d, leaf.ref)
			if err != nil {
				e.mu.Unlock()
				return err
			}
			roots = append(roots, queryLeaf{ref: ref, only: leaf.only})
		}
		e.mu.Unlock()
	}
	for {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		for _, leaf := range s.inheritanceRelations {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, leaf); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		var pending []queryLeaf
		err := inheritanceQueryLeaves(catalog, roots, s.op.session.token, func(leaf queryLeaf) error {
			if leaf.relation.relationKind != tableRelation {
				return nil
			}
			mode := lock.AccessShare
			if leaf.mark.mode != 0 {
				mode = lock.RowShare
			}
			// Named query roots already have exact-identity grants. Leave the
			// ordinary single-table path allocation- and reacquisition-free.
			for _, root := range roots {
				if root.ref == leaf.ref && root.relation != nil && root.relation.relationKind == tableRelation && (root.mark.mode != 0 || mode == lock.AccessShare) {
					return nil
				}
			}
			// Stored-base admission uses query rowmark modes: a present zero
			// entry already holds AccessShare, any nonzero entry holds RowShare.
			prior, granted := grants[leaf.ref]
			if granted && (prior != 0 || mode == lock.AccessShare) {
				return nil
			}
			if granted {
				for i := range pending {
					if pending[i].ref == leaf.ref {
						pending[i].mark = leaf.mark
						grants[leaf.ref] = leaf.mark.mode
						return nil
					}
				}
			}
			leaf.storage = e.privateStorageLocked(s.owner.catalog, leaf.ref.Address)
			pending = append(pending, leaf)
			if grants == nil {
				grants = make(map[ids.ObjectRef]lock.Mode)
			}
			grants[leaf.ref] = leaf.mark.mode
			return nil
		})
		if err == nil && len(pending) == 0 {
			s.inheritanceCatalog = catalog
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, leaf := range pending {
			mode := lock.AccessShare
			if grants[leaf.ref] != 0 {
				mode = lock.RowShare
			}
			policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
			request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: leaf.ref}, mode, policy)
			if request != nil {
				s.admissionWaited = true
				if s.replacementRead {
					e.replacementMu.RUnlock()
					s.replacementRead = false
				}
				err = request.Wait()
			}
			if err != nil {
				return err
			}
			s.owner.mu.Lock()
			implicit := s.owner.state == TxImplicit
			s.owner.mu.Unlock()
			if implicit && !s.replacementRead {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
			e.mu.Lock()
			_, err = currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.catalog), s.owner.catalog, leaf)
			if err == nil {
				e.markTemporaryAccessLocked(s, leaf.ref)
				s.inheritanceRelations = append(s.inheritanceRelations, leaf)
			}
			e.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func sourceOnly(statement parser.Statement) bool {
	if query, ok := statement.(*parser.Select); ok && query.From != nil {
		if table, ok := query.From.(*parser.TableReference); ok {
			return table.Only
		}
	}
	return false
}

func (s *StatementScope) inheritanceRead(current CatalogRead) CatalogRead {
	if s.inheritanceCatalog.state != nil {
		return s.inheritanceCatalog
	}
	return current
}
