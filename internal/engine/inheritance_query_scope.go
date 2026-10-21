package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

// Called under Engine.mu. Stored views expose their named source's scan mode,
// not a flattened set of leaves or the mode of an unrelated SQL occurrence.
func inheritanceQueryLeaves(view catalog.Read, roots []exec.Leaf, token ids.SessionToken, visit func(exec.Leaf) error) error {
	var active []ids.ObjectAddress
	var walk func(exec.Leaf) error
	walk = func(leaf exec.Leaf) error {
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
			for i, child := range stored.ViewOf(d).Sources() {
				if child.AnalysisOnly {
					continue
				}
				ref, err := stored.ViewUseReference(view, d, child.Ref)
				if err != nil {
					return err
				}
				if err := walk(exec.Leaf{Ref: ref, Only: child.Only, Mark: viewLeafRowMark(stored.ViewOf(d), i, leaf.Mark)}); err != nil {
					return err
				}
			}
			return nil
		}
		if d.RelationKind != catalog.TableRelation || leaf.Only {
			return visit(leaf)
		}
		return table.InheritanceQueryClosure(view, d, token, func(physical *catalog.Relation) error {
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
	e := s.engine()
	roots := s.querySources
	if s.materializedOrZero().refresh {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.Catalog)
		d, ok := catalog.Relation(s.lockedRef.Address)
		if !ok || d.View == nil {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		roots = append([]exec.Leaf(nil), roots...)
		for _, leaf := range stored.ViewOf(d).Sources() {
			if leaf.AnalysisOnly {
				continue
			}
			ref, err := stored.ViewUseReference(catalog, d, leaf.Ref)
			if err != nil {
				e.mu.Unlock()
				return err
			}
			roots = append(roots, exec.Leaf{Ref: ref, Only: leaf.Only})
		}
		e.mu.Unlock()
	}
	for {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.Catalog)
		for _, leaf := range s.inheritance.relations {
			if _, err := currentQuerySourceLocked(e, view, s.owner.Catalog, leaf); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		var pending []exec.Leaf
		err := inheritanceQueryLeaves(view, roots, s.op.Session.SessionToken, func(leaf exec.Leaf) error {
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
			leaf.Storage = e.privateStorageLocked(s.owner.Catalog, leaf.Ref.Address)
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
			policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
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
			s.owner.Mu.Lock()
			implicit := s.owner.State == session.TxImplicit
			s.owner.Mu.Unlock()
			if implicit && !s.replacementRead {
				e.replacementMu.RLock()
				s.replacementRead = true
			}
			e.mu.Lock()
			_, err = currentQuerySourceLocked(e, e.privateCatalogLocked(s.owner.Catalog), s.owner.Catalog, leaf)
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
	relations []exec.Leaf  // Exact physical SELECT grants, separate from SQL occurrences.
	catalog   catalog.Read // Immutable topology after the last grant/wait reconciliation.
	alter     *inheritanceAlterState
}

// endStatement drops the statement's physical grants and frozen topology.
// The ALTER state is released by its own handler.
func (i *inheritanceScope) endStatement() {
	i.relations, i.catalog = nil, catalog.Read{}
}
