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

// AccessShare on the named parent does not freeze CREATE INHERITS. Reconcile
// growth after every wait, then freeze one immutable topology for this statement.
// Previously admitted identities must survive; a same-name replacement is never
// a substitute. No manager wait is performed under Engine.mu.
func (s *StatementScope) acquireInheritanceSources(grants map[ids.ObjectRef]lock.Mode, imported ...[]exec.Leaf) error {
	e := s.Engine()
	roots := s.QuerySources
	if s.MaterializedOrZero().Refresh {
		e.Mu.Lock()
		catalog := e.PrivateCatalogLocked(s.Owner.Catalog)
		d, ok := catalog.Relation(s.LockedRef.Address)
		if !ok || d.View == nil {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		roots = append([]exec.Leaf(nil), roots...)
		for _, leaf := range stored.ViewOf(d).Sources() {
			if leaf.AnalysisOnly {
				continue
			}
			ref, err := stored.ViewUseReference(catalog, d, leaf.Ref)
			if err != nil {
				e.Mu.Unlock()
				return err
			}
			roots = append(roots, exec.Leaf{Ref: ref, Only: leaf.Only})
		}
		e.Mu.Unlock()
	}
	if len(imported) != 0 {
		roots = append(append([]exec.Leaf(nil), roots...), imported[0]...)
	}
	for {
		e.Mu.Lock()
		view := e.PrivateCatalogLocked(s.Owner.Catalog)
		for _, leaf := range s.Inheritance.relations {
			if _, err := CurrentQuerySourceLocked(e, view, s.Owner.Catalog, leaf); err != nil {
				e.Mu.Unlock()
				return err
			}
		}
		pending, next, err := table.InheritancePendingGrants(view, roots, s.Op.Session.SessionToken, grants)
		grants = next
		for i := range pending {
			pending[i].Storage = e.PrivateStorageLocked(s.Owner.Catalog, pending[i].Ref.Address)
		}
		if err == nil && len(pending) == 0 {
			s.Inheritance.catalog = view
		}
		e.Mu.Unlock()
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
			policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
			request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: leaf.Ref}, mode, policy)
			if request != nil {
				s.admissionWaited = true
				if len(s.rlsActive) != 0 {
					s.rlsImportWaits++
				}
				if s.ReplacementRead {
					e.ReplacementMu.RUnlock()
					s.ReplacementRead = false
				}
				err = request.Wait()
			}
			if err != nil {
				return err
			}
			s.Owner.Mu.Lock()
			implicit := s.Owner.State == session.TxImplicit
			s.Owner.Mu.Unlock()
			if implicit && !s.ReplacementRead {
				e.ReplacementMu.RLock()
				s.ReplacementRead = true
			}
			e.Mu.Lock()
			_, err = CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.Owner.Catalog), s.Owner.Catalog, leaf)
			if err == nil {
				e.markTemporaryAccessLocked(s, leaf.Ref, temporaryOwnAccess)
				s.Inheritance.relations = append(s.Inheritance.relations, leaf)
			}
			e.Mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func (s *StatementScope) inheritanceRead(current catalog.Read) catalog.Read {
	if s.Inheritance.catalog.State() != nil {
		return s.Inheritance.catalog
	}
	return current
}

// inheritanceScope is the statement's inheritance group: the exact physical
// SELECT grants, the topology frozen after the last reconciliation, and the
// ALTER TABLE recursion state. Every query admission writes it, so it is a
// value group rather than a pointer slot.
type inheritanceScope struct {
	relations     []exec.Leaf  // Exact physical SELECT grants, separate from SQL occurrences.
	catalog       catalog.Read // Immutable topology after the last grant/wait reconciliation.
	Alter         *InheritanceAlterState
	Relationships []InheritanceRelationshipAdmission
}

// endStatement drops the statement's physical grants and frozen topology.
// The ALTER state is released by its own handler.
func (i *inheritanceScope) endStatement() {
	i.relations, i.catalog = nil, catalog.Read{}
	i.Relationships = nil
}

// inheritancePlan is the recursing ALTER's plan record, nil when the statement
// is not recursing through an inheritance tree. Its methods are nil-safe.
func inheritancePlan(scope *StatementScope) *table.InheritanceAlterPlan {
	if state := scope.Inheritance.Alter; state != nil {
		return &state.InheritanceAlterPlan
	}
	return nil
}
