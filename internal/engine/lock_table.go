package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func explicitLockMode(mode parser.TableLockMode) relationLockMode {
	switch mode {
	case parser.LockAccessShare:
		return accessShare
	case parser.LockRowShare:
		return relationRowShare
	case parser.LockRowExclusive:
		return rowExclusive
	case parser.LockShareUpdateExclusive:
		return shareUpdateExclusive
	case parser.LockShare:
		return relationShare
	case parser.LockShareRowExclusive:
		return shareRowExclusive
	case parser.LockExclusive:
		return relationExclusive
	case parser.LockAccessExclusive:
		return accessExclusive
	}
	return 0
}

func checkExplicitLock(auth AuthorizationView, ref ids.ObjectRef, d *relationDescriptor, mode relationLockMode) error {
	if d.relationKind != tableRelation && d.relationKind != partitionedRelation && d.relationKind != viewRelation {
		return tableKind(d, "lock")
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	alternatives := authorization.MaintainPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege
	if mode == accessShare || mode == relationRowShare || mode == rowExclusive {
		alternatives |= authorization.InsertPrivilege
	}
	if mode == accessShare {
		alternatives |= authorization.SelectPrivilege
	}
	if rights&alternatives != 0 {
		if hierarchyRelation(d) {
			return sqlError("0A000", "locking partition hierarchies is unsupported")
		}
		return nil
	}
	kind := "table"
	if d.relationKind == viewRelation {
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
	policy := relationLockPolicy{timeoutSlot: &s.op.lockWait, nowait: stmt.Nowait, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	for _, target := range stmt.Targets {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ids.ObjectRef
		if err == nil && s.txRef.Epoch() != e.state.epoch {
			err = ErrCatalogReplaced
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
func (s *StatementScope) acquireExplicitRelation(stmt *parser.LockTable, ref ids.ObjectRef, name string, mode relationLockMode, policy relationLockPolicy, actor, invoker authorization.PrincipalRef, ancestors []ids.ObjectRef, only bool) error {
	for _, ancestor := range ancestors {
		if ancestor == ref {
			return nil
		}
	}
	e := s.op.session.Engine()
	inspect := func() (*relationDescriptor, error) {
		if ref.Domain() != e.domain || ref.Epoch != e.state.epoch || s.txRef.Epoch() != e.state.epoch {
			return nil, ErrCatalogReplaced
		}
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		if err != nil {
			return nil, err
		}
		auth.state.CurrentUser = actor
		d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if !ok {
			return nil, ErrCatalogReplaced
		}
		if !only || d.view != nil {
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
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref}, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
		return sqlError("55P03", fmt.Sprintf("could not obtain lock on relation \"%s\"", name))
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
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
	if d.relationKind != viewRelation {
		return nil
	}
	principal := d.owner
	if d.view.invoker {
		principal = invoker
	}
	ancestors = append(ancestors, ref)
	for _, source := range d.view.sources {
		e.mu.Lock()
		catalog := e.privateCatalogLocked(s.owner.catalog)
		child, ok := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
		use, useErr := storedViewUseReference(catalog, d, source.ref)
		valid := ok && useErr == nil
		e.mu.Unlock()
		if !valid {
			return ErrCatalogReplaced
		}
		if child.relationKind == materializedViewRelation {
			return tableKind(child, "lock")
		}
		if child.relationKind != tableRelation && child.relationKind != partitionedRelation && child.relationKind != viewRelation {
			continue
		}
		if err := s.acquireExplicitRelation(stmt, use, child.Name(), mode, policy, principal, invoker, ancestors, false); err != nil {
			return err
		}
	}
	return nil
}
