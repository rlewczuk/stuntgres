package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (s *StatementScope) acquireIndexMaintenance(stmt parser.Statement) error {
	e := s.engine()
	names, ifExists, drop, err := index.MaintenanceNames(stmt)
	if err != nil {
		return err
	}
	g := &index.MaintenanceGrant{Descriptors: make(map[ids.ObjectAddress]*catalog.Relation)}
	e.mu.Lock()
	fail := func(err error) error { e.mu.Unlock(); return err }
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err != nil {
		return fail(err)
	}
	for _, name := range names {
		ref, err := e.resolveRelationReference(auth, name, true)
		if err := g.AddTarget(auth.Catalog, name, ref, err, err != nil && table.RenameMissing(err), drop, ifExists); err != nil {
			return fail(err)
		}
	}
	if err := g.CollectGroup(auth.View, auth, auth.Catalog, stmt); err != nil {
		return fail(err)
	}
	if v, ok := g.NeedsAttach(stmt); ok {
		ref, err := e.resolveRelationReference(auth, v.Partition, true)
		if err != nil {
			return fail(err)
		}
		if err := g.AddAttach(s.op.Context(), auth.Catalog, ref); err != nil {
			return fail(err)
		}
	}
	if err := g.FinishPlan(auth.View, stmt); err != nil {
		return fail(err)
	}
	s.indexes().Maintenance = g
	e.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	g.SortLockOrder()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	acquire := func(ref ids.ObjectRef, mode lock.Mode) error {
		request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
		if request != nil {
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.statementAuthorizationLocked(s.op, stmt)
		if err != nil {
			return err
		}
		if s.txRef.Epoch() != e.catalog.state.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if err := g.Check(auth.View, auth, stmt, false); err != nil {
			return err
		}
		for address, observed := range g.Descriptors {
			current := auth.Catalog.MustRelation(address)
			if g.Partition && current != observed {
				return ids.ErrCatalogReplaced
			}
			g.Descriptors[address] = current
		}
		return nil
	}
	parentMode := g.ParentMode(drop)
	for _, ref := range g.Parents {
		if err := acquire(ref, parentMode); err != nil {
			return err
		}
	}
	for _, ref := range g.Indexes {
		if err := acquire(ref, lock.AccessExclusive); err != nil {
			return err
		}
	}
	if ref, mode, ok := g.AlterTarget(stmt); ok {
		if !slices.Contains(g.Indexes, ref) {
			if err := acquire(ref, mode); err != nil {
				return err
			}
		}
		s.lockedRef = ref
		s.lockedDescriptor = g.Descriptors[ref.Address]
		s.lockedIdentityOnly = true
	}
	return nil
}
