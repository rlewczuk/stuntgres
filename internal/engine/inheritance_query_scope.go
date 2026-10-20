package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

// Closure is physical-identity deduplication within one SQL occurrence. A foreign
// session's temporary branch is not admitted, even through a persistent ancestor.
func inheritanceQueryClosure(view catalog.Read, root *catalog.Relation, token ids.SessionToken, visit func(*catalog.Relation) error) error {
	if err := visit(root); err != nil {
		return err
	}
	children := inheritanceChildren(view, root.Address())
	if len(children) == 0 {
		return nil
	}
	seen := map[ids.ObjectAddress]bool{root.Address(): true}
	var walk func([]*catalog.Relation) error
	walk = func(children []*catalog.Relation) error {
		for _, child := range children {
			if seen[child.Address()] {
				continue
			}
			seen[child.Address()] = true
			ns, ok := view.Namespace(child.Namespace())
			if !ok {
				return ids.ErrCatalogReplaced
			}
			if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
			if err := walk(inheritanceChildren(view, child.Address())); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(children)
}

// Called under Engine.mu. Stored views expose their named source's scan mode,
// not a flattened set of leaves or the mode of an unrelated SQL occurrence.
func inheritanceQueryLeaves(view catalog.Read, roots []queryLeaf, token ids.SessionToken, visit func(queryLeaf) error) error {
	var active []ids.ObjectAddress
	var walk func(queryLeaf) error
	walk = func(leaf queryLeaf) error {
		d, ok := view.Relation(leaf.Ref.Address)
		if !ok || leaf.Ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		leaf.Relation = d
		if d.RelationKind == catalog.ViewRelation {
			for _, address := range active {
				if address == d.Address() {
					return nil
				}
			}
			active = append(active, d.Address())
			defer func() { active = active[:len(active)-1] }()
			for i, child := range viewOf(d).sources {
				if child.analysisOnly {
					continue
				}
				ref, err := storedViewUseReference(view, d, child.ref)
				if err != nil {
					return err
				}
				if err := walk(queryLeaf{Ref: ref, Only: child.only, Mark: viewLeafRowMark(viewOf(d), i, leaf.Mark)}); err != nil {
					return err
				}
			}
			return nil
		}
		if d.RelationKind != catalog.TableRelation || leaf.Only {
			return visit(leaf)
		}
		return inheritanceQueryClosure(view, d, token, func(physical *catalog.Relation) error {
			child := leaf
			child.Ref.Address, child.Relation = physical.Address(), physical
			return visit(child)
		})
	}
	for _, leaf := range roots {
		if !leaf.AnalysisOnly {
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
	if s.materializedOrZero().refresh {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		d, ok := catalog.Relation(s.lockedRef.Address)
		if !ok || d.View == nil {
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
			roots = append(roots, queryLeaf{Ref: ref, Only: leaf.only})
		}
		e.mu.Unlock()
	}
	for {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.catalog)
		for _, leaf := range s.inheritance.relations {
			if _, err := currentQuerySourceLocked(e, view, s.owner.catalog, leaf); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		var pending []queryLeaf
		err := inheritanceQueryLeaves(view, roots, s.op.session.token, func(leaf queryLeaf) error {
			if leaf.Relation.RelationKind != catalog.TableRelation {
				return nil
			}
			mode := lock.AccessShare
			if leaf.Mark.Mode != 0 {
				mode = lock.RowShare
			}
			// Named query roots already have exact-identity grants. Leave the
			// ordinary single-table path allocation- and reacquisition-free.
			for _, root := range roots {
				if root.Ref == leaf.Ref && root.Relation != nil && root.Relation.RelationKind == catalog.TableRelation && (root.Mark.Mode != 0 || mode == lock.AccessShare) {
					return nil
				}
			}
			// Stored-base admission uses query rowmark modes: a present zero
			// entry already holds AccessShare, any nonzero entry holds RowShare.
			prior, granted := grants[leaf.Ref]
			if granted && (prior != 0 || mode == lock.AccessShare) {
				return nil
			}
			if granted {
				for i := range pending {
					if pending[i].Ref == leaf.Ref {
						pending[i].Mark = leaf.Mark
						grants[leaf.Ref] = leaf.Mark.Mode
						return nil
					}
				}
			}
			leaf.Storage = e.privateStorageLocked(s.owner.catalog, leaf.Ref.Address)
			pending = append(pending, leaf)
			if grants == nil {
				grants = make(map[ids.ObjectRef]lock.Mode)
			}
			grants[leaf.Ref] = leaf.Mark.Mode
			return nil
		})
		if err == nil && len(pending) == 0 {
			s.inheritance.catalog = view
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
			if grants[leaf.Ref] != 0 {
				mode = lock.RowShare
			}
			policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
			request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: leaf.Ref}, mode, policy)
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
				e.markTemporaryAccessLocked(s, leaf.Ref)
				s.inheritance.relations = append(s.inheritance.relations, leaf)
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

func (s *StatementScope) inheritanceRead(current catalog.Read) catalog.Read {
	if s.inheritance.catalog.State() != nil {
		return s.inheritance.catalog
	}
	return current
}

// inheritanceScope is the statement's inheritance group: the exact physical
// SELECT grants, the topology frozen after the last reconciliation, and the
// ALTER TABLE recursion state. Every query admission writes it, so it is a
// value group rather than a pointer slot.
type inheritanceScope struct {
	relations []queryLeaf  // Exact physical SELECT grants, separate from SQL occurrences.
	catalog   catalog.Read // Immutable topology after the last grant/wait reconciliation.
	alter     *inheritanceAlterState
}

// endStatement drops the statement's physical grants and frozen topology.
// The ALTER state is released by its own handler.
func (i *inheritanceScope) endStatement() {
	i.relations, i.catalog = nil, catalog.Read{}
}
