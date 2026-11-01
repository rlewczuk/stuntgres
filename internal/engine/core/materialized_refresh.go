package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func (s *StatementScope) acquireMaterializedRefresh(stmt *parser.RefreshMaterializedView) error {
	// Utility entry selects the pair before target/source lock waits. Direct
	// internal callers share the same boundary as SQL execution.
	if err := s.snapshotRefreshPhase(stmt); err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	var ref ids.ObjectRef
	var observed *catalog.Relation
	if err == nil {
		ref, err = e.ResolveRelationReference(auth, stmt.Name, true)
	}
	if err == nil {
		err = view.MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil {
		observed = auth.Catalog.MustRelation(ref.Address)
		if captured := s.MaterializedOrZero().Definition; captured != nil && !view.MaterializedRefreshCompatible(captured, observed) {
			err = ids.ErrCatalogReplaced
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	mode := lock.AccessExclusive
	if stmt.Concurrent.Character != 0 {
		mode = lock.Exclusive
	}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
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
	if !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	e.Mu.Lock()
	auth, err = e.AuthorizationLocked(s.Op)
	var current *catalog.Relation
	if err == nil {
		current, _ = auth.Catalog.Relation(ref.Address)
	}
	if err == nil && (s.TxRef.Epoch() != e.Catalog.State.Epoch() || !view.MaterializedRefreshCompatible(observed, current)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = view.MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil && !stmt.NoData && !s.MaterializedOrZero().ExportRoots && s.exportCapture == nil && !s.Owner.Isolation.Stable() && refreshExportRootDemand(auth.Catalog, current) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && stmt.Concurrent.Character != 0 {
		err = view.MaterializedConcurrentAdmission(auth.Catalog, current, stmt.NoData)
	}
	if err == nil {
		s.LockedRef, s.LockedDescriptor, s.LockedStorage = ref, current, auth.StorageLocked(ref.Address)
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if !stmt.NoData {
		s.materialized().Refresh = true
		_, err := s.AcquireViewSources(false)
		return err
	}
	return nil
}

// validateMaterializedReplacement checks the stage's REFRESH replacement.
func (s *CatalogStage) validateMaterializedReplacement() error {
	return view.ValidateMaterializedReplacement(s.MaterializedOrZero().Replacement, &s.Candidate)
}

// materializedReplacementCurrent checks that the stage's REFRESH replacement
// still applies to snapshot and stored.
func (s *CatalogStage) materializedReplacementCurrent(snapshot *catalog.State, stored *storage.Relation) error {
	return s.MaterializedOrZero().Replacement.Current(&s.Candidate, snapshot, stored)
}

// materialized returns the statement's materialized slot, allocating it on first use.
func (s *StatementScope) materialized() *view.MaterializedRefreshScope {
	if s.MaterializedSlot == nil {
		s.MaterializedSlot = new(view.MaterializedRefreshScope)
	}
	return s.MaterializedSlot
}

// MaterializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *StatementScope) MaterializedOrZero() view.MaterializedRefreshScope {
	if s.MaterializedSlot == nil {
		return view.MaterializedRefreshScope{}
	}
	return *s.MaterializedSlot
}

func (s *StatementScope) snapshotRefreshPhase(stmt *parser.RefreshMaterializedView) error {
	return s.snapshotDataPhaseForUtility(stmt)
}

// Pure metadata classification in the same Engine.mu window as the utility's
// public/data cut. Ordinary resolution/ACL errors remain at their existing
// admission sites; no authorization, preflight or expression is executed here.
func (s *StatementScope) installRefreshExportDemandLocked(stmt *parser.RefreshMaterializedView) {
	if stmt.NoData { return }
	e := s.Engine()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil { return }
	ref, err := e.ResolveRelationReference(auth, stmt.Name, true)
	if err != nil { return }
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok || relation.RelationKind != catalog.MaterializedViewRelation { return }
	state := s.materialized()
	state.Definition = relation
	state.ExportRoots = refreshExportRootDemand(auth.Catalog, relation)
	s.exportRootDemand = s.exportRootDemand || state.ExportRoots
}

func refreshExportRootDemand(read catalog.Read, relation *catalog.Relation) bool {
	return stored.ReadExportRootDemand(read, stored.ViewOf(relation))
}
