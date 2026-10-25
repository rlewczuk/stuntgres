package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) acquireMaterializedRefresh(stmt *parser.RefreshMaterializedView) error {
	// Utility entry selects the pair before target/source lock waits. Direct
	// internal callers share the same boundary as SQL execution.
	if err := s.snapshotDataPhase(); err != nil {
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

// Engine.mu is held on entry, and is consumed on every return.
func refreshMaterializedLocked(e *Engine, scope *StatementScope, caller AuthorizationView, stmt *parser.RefreshMaterializedView, count int) (Result, error) {
	if count != 0 {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: REFRESH MATERIALIZED VIEW takes no arguments")
	}
	ref := scope.LockedRef
	if err := view.MaterializedRefreshAccess(caller.View, caller, ref); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	d := caller.Catalog.MustRelation(ref.Address)
	if d != scope.LockedDescriptor || caller.StorageLocked(ref.Address) != scope.LockedStorage {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	concurrent := stmt.Concurrent.Character != 0
	var indices []*catalog.Relation
	var previous storage.Relation
	var oldEntries []storage.IndexEntry
	var ownRows []storage.RowVersion
	var ownDeletes *visibility.PrivateDeletes
	frontier := e.Visibility.CommitSeq()
	if concurrent {
		indices = view.MaterializedEligibleIndexes(caller.Catalog, d)
		previous = *scope.LockedStorage
		var root *storage.IndexRoot
		if len(indices) != 0 {
			root = index.EffectiveRoot(e.Storage.IndexRoots, scope.Owner.Catalog, indices[0].Address())
		}
		if root == nil {
			e.Mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: concurrent materialized index root is missing")
		}
		oldEntries = root.Entries
		ownRows, ownDeletes = scope.ColumnProjectionRoots(d.Address())
	}
	stage := e.BeginCatalogStage(caller.Catalog)
	stage.AuthorizeLocked(caller, scope.Op.Session)
	stage.QuerySources = scope.MaterializedOrZero().Sources
	materializedStage(stage).SourceView = scope.MaterializedOrZero().SourceView
	stage.Indexes().Output = scope.Output
	defer e.DiscardCatalogStage(stage)
	var rows [][]sqltype.Value
	if stmt.NoData {
		e.Mu.Unlock()
	} else {
		var err error
		rows, err = materializedRefreshRowsLocked(e, scope, ref, d)
		if err != nil {
			return Result{}, err
		}
	}
	if concurrent {
		var err error
		if len(ownRows) != 0 || ownDeletes != nil {
			previous = view.ConcurrentPrevious(previous, ownRows, ownDeletes, frontier)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: stage.Base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			oldEntries, err = index.BuildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return Result{}, err
			}
		}
		rows, err = view.MaterializedConcurrentRows(scope.Op.Context(), caller.Catalog, d, &previous, frontier, oldEntries, indices, rows, scope.Output)
		if err != nil {
			return Result{}, err
		}
	}
	if err := stageMaterializedRows(e, scope.Op.Context(), stage, d, scope.LockedStorage, rows, !stmt.NoData, scope.TxRef); err != nil {
		return Result{}, err
	}
	materializedStage(stage).Replacement.Concurrent = concurrent
	// Provider validation may replace the catalog. Exact stage fences, not this
	// admission lease, decide whether this original target can still publish.
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.Completed = true
	}
	return Result{}, err
}

// Population's effective principal and expression identity are restored before
// any stage validation/publication. No session settings or session_user change.
func materializedRefreshRowsLocked(e *Engine, scope *StatementScope, ref ids.ObjectRef, d *catalog.Relation) ([][]sqltype.Value, error) {
	op := scope.Op
	population, owner := OperationReplayOrZero(op).MaterializedPopulation, OperationReplayOrZero(op).MaterializedOwner
	identity, schema := scope.Identity, scope.Inquiry.CurrentSchemaResult
	input := scope.Input
	OperationReplay(op).MaterializedPopulation, OperationReplay(op).MaterializedOwner = true, d.Owner
	scope.Inquiry.CurrentSchemaResult = sqltype.Value{}
	defer func() {
		OperationReplay(op).MaterializedPopulation, OperationReplay(op).MaterializedOwner = population, owner
		scope.Identity, scope.Inquiry.CurrentSchemaResult = identity, schema
		scope.Input = input
	}()
	auth, err := e.AuthorizationLocked(op)
	if err == nil {
		err = scope.CaptureIdentityLocked(auth)
	}
	if err == nil {
		// Preserve operation settings, but never borrow caller catalog authority
		// for the population owner's restricted reference path.
		candidate := input
		candidate.Regclass, candidate.Regtype, candidate.Regrole = nil, nil, nil
		candidate.Operator, candidate.Regproc, candidate.ACLItem = nil, nil, nil
		candidate.FTS = nil
		candidate.RegTS = nil
		candidate.TypeDiagnosticName = nil
		CaptureEnumOperationSafety(op, &candidate)
		err = stored.CaptureRetainedInput(auth.Catalog, stored.ViewOf(d),
			func() error { return e.CaptureElementInput(auth, op, &candidate) },
			func() error {
				if candidate.RegTS != nil { return nil }
				var err error
				candidate.RegTS, err = CaptureRegTSInput(auth)
				return err
			},
			func(typ *sqltype.Descriptor) error { return e.CaptureTypeDiagnosticInput(auth, op, &candidate, typ) })
		if err == nil {
			scope.Input = candidate
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	root, source, err := e.PrepareStoredQueryLocked(scope, ref, stored.ViewOf(d), d.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		return nil, err
	}
	result, _, err := exec.Finalize(op.Context(), root, source, nil)
	if err != nil {
		return nil, err
	}
	return result.(*exec.FinalRows).TakeTableRows(op.Context())
}

func stageMaterializedRows(e *Engine, ctx context.Context, s *CatalogStage, d *catalog.Relation, previous *storage.Relation, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if s == nil || s.Owner != e || s.Lifecycle != CatalogOpen || s.Invalid != nil || s.MaterializedOrZero().Replacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.StageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.StageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement stage")
	}
	if s.Base.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.Revision() == ids.DefinitionRevision(^uint64(0)) || previous.Generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: materialized replacement generation exhausted")
	}
	if err := view.CheckMaterializedRows(d, rows); err != nil {
		return err
	}
	versions, err := e.Storage.AllocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	next := *d
	next.ObjectRevision++
	next.Populated = populated
	if err := s.ReplaceDescriptor(&next, false, false); err != nil {
		return err
	}
	materializedStage(s).Replacement = &view.MaterializedReplacement{Original: d, Descriptor: &next, Predecessor: previous, Storage: &storage.Relation{Versions: versions, Generation: previous.Generation + 1}, Generation: previous.Generation, Length: len(previous.Versions), Ends: previous.Ends}
	materializedStage(s).Replacement.IndexCount = view.MaterializedIndexCount(s.Base, d.Address())
	if s.IndexesOrZero().Sets == nil {
		s.Indexes().Sets = make(map[ids.ObjectAddress]int)
	}
	s.Indexes().Sets[d.Address()] = s.MaterializedOrZero().Replacement.IndexCount
	s.InitialTx = tx
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
	if s.materializedSlot == nil {
		s.materializedSlot = new(view.MaterializedRefreshScope)
	}
	return s.materializedSlot
}

// MaterializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *StatementScope) MaterializedOrZero() view.MaterializedRefreshScope {
	if s.materializedSlot == nil {
		return view.MaterializedRefreshScope{}
	}
	return *s.materializedSlot
}
