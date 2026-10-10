package engine

import (
	"errors"
	"fmt"
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

func checkExplicitLock(auth AuthorizationView, ref ObjectRef, d *relationDescriptor, mode relationLockMode) error {
	if d.relationKind != tableRelation && d.view == nil {
		return tableKind(d, "lock")
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return err
	}
	alternatives := MaintainPrivilege | UpdatePrivilege | DeletePrivilege | TruncatePrivilege
	if mode == accessShare || mode == relationRowShare || mode == rowExclusive {
		alternatives |= InsertPrivilege
	}
	if mode == accessShare {
		alternatives |= SelectPrivilege
	}
	if rights&alternatives != 0 {
		return nil
	}
	kind := "table"
	if d.view != nil {
		kind = "view"
	}
	return sqlError("42501", fmt.Sprintf("permission denied for %s %s", kind, d.Name()))
}

func (s *StatementScope) acquireExplicitLocks(stmt *parser.LockTable) error {
	s.owner.mu.Lock()
	active := s.owner.state == TxActive
	s.owner.mu.Unlock()
	if !active {
		return sqlError("25P01", "LOCK TABLE can only be used in transaction blocks")
	}
	e := s.op.session.Engine()
	mode := explicitLockMode(stmt.Mode)
	policy := relationLockPolicy{operation: s.op, nowait: stmt.Nowait, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	for _, target := range stmt.Targets {
		e.mu.Lock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		var ref ObjectRef
		if err == nil && s.txRef.epoch != e.state.epoch {
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
		// Native tables have no inheritance descriptors. ONLY and star therefore
		// select this standalone table; view definitions always recurse.
		if err := s.acquireExplicitRelation(stmt, ref, objectNameText(target.Name), mode, policy, invoker, invoker, nil); err != nil {
			return err
		}
	}
	return nil
}

// Bound view sources never repeat namespace lookup. Each grant retains the
// original identity and rechecks the principal against grant-time metadata.
func (s *StatementScope) acquireExplicitRelation(stmt *parser.LockTable, ref ObjectRef, name string, mode relationLockMode, policy relationLockPolicy, actor, invoker PrincipalRef, ancestors []ObjectRef) error {
	for _, ancestor := range ancestors {
		if ancestor == ref {
			return nil
		}
	}
	e := s.op.session.Engine()
	inspect := func() (*relationDescriptor, error) {
		if ref.owner != e.owner || ref.Epoch != e.state.epoch || s.txRef.epoch != e.state.epoch {
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
	if d.view == nil {
		return nil
	}
	principal := d.owner
	if d.view.invoker {
		principal = invoker
	}
	ancestors = append(ancestors, ref)
	for _, source := range d.view.sources {
		e.mu.Lock()
		child, ok := e.privateCatalogLocked(s.owner.catalog).state.descriptors[source.ref.Address].(*relationDescriptor)
		valid := source.ref.owner == e.owner && source.ref.Epoch == e.state.epoch && ok
		e.mu.Unlock()
		if !valid {
			return ErrCatalogReplaced
		}
		if child.relationKind != tableRelation && child.view == nil {
			continue
		}
		if err := s.acquireExplicitRelation(stmt, source.ref, child.Name(), mode, policy, principal, invoker, ancestors); err != nil {
			return err
		}
	}
	return nil
}
