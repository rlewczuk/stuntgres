package core

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (s *StatementScope) acquireExplicitLocks(stmt *parser.LockTable) error {
	s.Owner.Mu.Lock()
	active := s.Owner.InBlock()
	s.Owner.Mu.Unlock()
	if !active {
		return SQLError("25P01", "LOCK TABLE can only be used in transaction blocks")
	}
	e := s.Engine()
	mode := table.ExplicitLockMode(stmt.Mode)
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, NoWait: stmt.Nowait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	for _, target := range stmt.Targets {
		e.Mu.Lock()
		auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
		var ref ids.ObjectRef
		if err == nil && s.TxRef.Epoch() != e.Catalog.State.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.ResolveRelationReference(auth, target.Name, true)
		}
		invoker := auth.State.CurrentUser
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.acquireExplicitRelation(stmt, ref, catalog.ObjectNameText(target.Name), mode, policy, invoker, invoker, nil, target.Only); err != nil {
			return err
		}
	}
	return nil
}

// Bound view sources never repeat namespace lookup. Each grant retains the
// original identity and rechecks the principal against grant-time metadata.
func (s *StatementScope) acquireExplicitRelation(stmt *parser.LockTable, ref ids.ObjectRef, name string, mode lock.Mode, policy lock.Policy, actor, invoker authorization.PrincipalRef, ancestors []ids.ObjectRef, only bool) error {
	for _, ancestor := range ancestors {
		if ancestor == ref {
			return nil
		}
	}
	e := s.Engine()
	inspect := func() (*catalog.Relation, error) {
		if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || s.TxRef.Epoch() != e.Catalog.State.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
		if err != nil {
			return nil, err
		}
		auth.State.CurrentUser = actor
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if !only || d.View != nil {
			if err := table.InheritanceTargetGuard(auth.Catalog, d); err != nil {
				return nil, err
			}
		}
		return d, table.CheckExplicitLock(auth.View, auth, ref, d, mode)
	}
	e.Mu.Lock()
	_, err := inspect()
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		return SQLError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", name))
	}
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	e.Mu.Lock()
	d, err := inspect()
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if d.RelationKind != catalog.ViewRelation {
		return nil
	}
	principal := d.Owner
	if stored.ViewOf(d).Invoker() {
		principal = invoker
	}
	ancestors = append(ancestors, ref)
	for _, source := range stored.ViewOf(d).Sources() {
		e.Mu.Lock()
		view := e.PrivateCatalogLocked(s.Owner.Catalog)
		child, ok := view.Relation(source.Ref.Address)
		use, useErr := stored.ViewUseReference(view, d, source.Ref)
		valid := ok && useErr == nil
		e.Mu.Unlock()
		if !valid {
			return ids.ErrCatalogReplaced
		}
		if child.RelationKind == catalog.MaterializedViewRelation {
			return analyze.TableKindError(child, "lock")
		}
		if child.RelationKind != catalog.TableRelation && child.RelationKind != catalog.PartitionedRelation && child.RelationKind != catalog.ViewRelation {
			continue
		}
		if err := s.acquireExplicitRelation(stmt, use, child.Name(), mode, policy, principal, invoker, ancestors, false); err != nil {
			return err
		}
	}
	return nil
}
