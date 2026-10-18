package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
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

func checkExplicitLock(auth AuthorizationView, ref ids.ObjectRef, d *relationDescriptor, mode lock.Mode) error {
	if d.RelationKind != tableRelation && d.RelationKind != partitionedRelation && d.RelationKind != viewRelation {
		return tableKind(d, "lock")
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
		if hierarchyRelation(d) {
			return sqlError("0A000", "locking partition hierarchies is unsupported")
		}
		return nil
	}
	kind := "table"
	if d.RelationKind == viewRelation {
		kind = "view"
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, d.Name()))
}

func (s *StatementScope) acquireExplicitLocks(stmt *parser.LockTable) error {
	s.owner.mu.Lock()
	active := s.owner.inBlock()
	s.owner.mu.Unlock()
	if !active {
		return sqlError("25P01", "LOCK TABLE can only be used in transaction blocks")
	}
	e := s.op.session.Engine()
	mode := explicitLockMode(stmt.Mode)
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, NoWait: stmt.Nowait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	for _, target := range stmt.Targets {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ids.ObjectRef
		if err == nil && s.txRef.Epoch() != e.catalog.state.epoch {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			ref, err = e.resolveRelationReference(auth, target.Name, true)
		}
		invoker := auth.state.CurrentUser
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if err := s.acquireExplicitRelation(stmt, ref, objectNameText(target.Name), mode, policy, invoker, invoker, nil, target.Only); err != nil {
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
	e := s.op.session.Engine()
	inspect := func() (*relationDescriptor, error) {
		if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.epoch || s.txRef.Epoch() != e.catalog.state.epoch {
			return nil, ids.ErrCatalogReplaced
		}
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		if err != nil {
			return nil, err
		}
		auth.state.CurrentUser = actor
		d, ok := auth.catalog.Relation(ref.Address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if !only || d.View != nil {
			if err := inheritanceTargetGuard(auth.catalog, d); err != nil {
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
	if d.RelationKind != viewRelation {
		return nil
	}
	principal := d.Owner
	if viewOf(d).invoker {
		principal = invoker
	}
	ancestors = append(ancestors, ref)
	for _, source := range viewOf(d).sources {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		child, ok := catalog.Relation(source.ref.Address)
		use, useErr := storedViewUseReference(catalog, d, source.ref)
		valid := ok && useErr == nil
		e.mu.Unlock()
		if !valid {
			return ids.ErrCatalogReplaced
		}
		if child.RelationKind == materializedViewRelation {
			return tableKind(child, "lock")
		}
		if child.RelationKind != tableRelation && child.RelationKind != partitionedRelation && child.RelationKind != viewRelation {
			continue
		}
		if err := s.acquireExplicitRelation(stmt, use, child.Name(), mode, policy, principal, invoker, ancestors, false); err != nil {
			return err
		}
	}
	return nil
}
