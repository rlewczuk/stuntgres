package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func explicitLockMode(mode parser.TableLockMode) lock.Mode {
	switch mode {
	case parser.LockAccessShare:
		return lock.AccessShare
	case parser.LockRowShare:
		return lock.RowShare
	case parser.LockRowExclusive:
		return lock.RowExclusive
	case parser.LockShareUpdateExclusive:
		return lock.ShareUpdateExclusive
	case parser.LockShare:
		return lock.Share
	case parser.LockShareRowExclusive:
		return lock.ShareRowExclusive
	case parser.LockExclusive:
		return lock.Exclusive
	case parser.LockAccessExclusive:
		return lock.AccessExclusive
	}
	return 0
}

func checkExplicitLock(auth AuthorizationView, ref ids.ObjectRef, d *catalog.Relation, mode lock.Mode) error {
	if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.ViewRelation {
		return analyze.TableKindError(d, "lock")
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	alternatives := authorization.MaintainPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege
	if mode == lock.AccessShare || mode == lock.RowShare || mode == lock.RowExclusive {
		alternatives |= authorization.InsertPrivilege
	}
	if mode == lock.AccessShare {
		alternatives |= authorization.SelectPrivilege
	}
	if rights&alternatives != 0 {
		if catalog.HierarchyRelation(d) {
			return sqlError("0A000", "locking partition hierarchies is unsupported")
		}
		return nil
	}
	kind := "table"
	if d.RelationKind == catalog.ViewRelation {
		kind = "view"
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, d.Name()))
}

func (s *StatementScope) acquireExplicitLocks(stmt *parser.LockTable) error {
	s.owner.Mu.Lock()
	active := s.owner.InBlock()
	s.owner.Mu.Unlock()
	if !active {
		return sqlError("25P01", "LOCK TABLE can only be used in transaction blocks")
	}
	e := s.engine()
	mode := explicitLockMode(stmt.Mode)
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, NoWait: stmt.Nowait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	for _, target := range stmt.Targets {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ids.ObjectRef
		if err == nil && s.txRef.Epoch() != e.catalog.state.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.resolveRelationReference(auth, target.Name, true)
		}
		invoker := auth.State.CurrentUser
		e.mu.Unlock()
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
	e := s.engine()
	inspect := func() (*catalog.Relation, error) {
		if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != e.catalog.state.Epoch() {
			return nil, ids.ErrCatalogReplaced
		}
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		if err != nil {
			return nil, err
		}
		auth.State.CurrentUser = actor
		d, ok := auth.Catalog.Relation(ref.Address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if !only || d.View != nil {
			if err := inheritanceTargetGuard(auth.Catalog, d); err != nil {
				return nil, err
			}
		}
		return d, checkExplicitLock(auth, ref, d, mode)
	}
	e.mu.Lock()
	_, err := inspect()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		return sqlError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", name))
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	e.mu.Lock()
	d, err := inspect()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if d.RelationKind != catalog.ViewRelation {
		return nil
	}
	principal := d.Owner
	if ViewOf(d).Invoker() {
		principal = invoker
	}
	ancestors = append(ancestors, ref)
	for _, source := range ViewOf(d).Sources() {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.Catalog)
		child, ok := view.Relation(source.Ref.Address)
		use, useErr := ViewUseReference(view, d, source.Ref)
		valid := ok && useErr == nil
		e.mu.Unlock()
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
