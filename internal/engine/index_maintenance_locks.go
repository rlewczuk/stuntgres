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
	e := s.Engine()
	names, ifExists, drop, err := index.MaintenanceNames(stmt)
	if err != nil {
		return err
	}
	g := &index.MaintenanceGrant{Descriptors: make(map[ids.ObjectAddress]*catalog.Relation)}
	e.Mu.Lock()
	fail := func(err error) error { e.Mu.Unlock(); return err }
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
	if err != nil {
		return fail(err)
	}
	for _, name := range names {
		ref, err := e.ResolveRelationReference(auth, name, true)
		if err := g.AddTarget(auth.Catalog, name, ref, err, err != nil && table.RenameMissing(err), drop, ifExists); err != nil {
			return fail(err)
		}
	}
	if err := g.CollectGroup(auth.View, auth, auth.Catalog, stmt); err != nil {
		return fail(err)
	}
	if v, ok := g.NeedsAttach(stmt); ok {
		ref, err := e.ResolveRelationReference(auth, v.Partition, true)
		if err != nil {
			return fail(err)
		}
		if err := g.AddAttach(s.Op.Context(), auth.Catalog, ref); err != nil {
			return fail(index.DefaultClassDiagnostic(DDLHost{E: e, Op: s.Op}, auth, err))
		}
	}
	if err := g.FinishPlan(auth.View, stmt); err != nil {
		return fail(err)
	}
	s.indexes().Maintenance = g
	e.Mu.Unlock()
	if s.ReplacementRead {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	g.SortLockOrder()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	acquire := func(ref ids.ObjectRef, mode lock.Mode) error {
		request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
		if request != nil {
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.StatementAuthorizationLocked(s.Op, stmt)
		if err != nil {
			return err
		}
		if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
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
		s.LockedRef = ref
		s.LockedDescriptor = g.Descriptors[ref.Address]
		s.lockedIdentityOnly = true
	}
	return nil
}
