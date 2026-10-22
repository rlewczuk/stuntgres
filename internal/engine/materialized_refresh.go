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
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var ref ids.ObjectRef
	var observed *catalog.Relation
	if err == nil {
		ref, err = e.resolveRelationReference(auth, stmt.Name, true)
	}
	if err == nil {
		err = view.MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil {
		observed = auth.Catalog.MustRelation(ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	mode := lock.AccessExclusive
	if stmt.Concurrent.Character != 0 {
		mode = lock.Exclusive
	}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref}, mode, policy)
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
	if !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	auth, err = e.authorizationLocked(s.op)
	var current *catalog.Relation
	if err == nil {
		current, _ = auth.Catalog.Relation(ref.Address)
	}
	if err == nil && (s.txRef.Epoch() != e.catalog.state.Epoch() || !view.MaterializedRefreshCompatible(observed, current)) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = view.MaterializedRefreshAccess(auth.View, auth, ref)
	}
	if err == nil && stmt.Concurrent.Character != 0 {
		err = view.MaterializedConcurrentAdmission(auth.Catalog, current, stmt.NoData)
	}
	if err == nil {
		s.lockedRef, s.lockedDescriptor, s.lockedStorage = ref, current, auth.StorageLocked(ref.Address)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if !stmt.NoData {
		s.materialized().Refresh = true
		return s.acquireViewSources()
	}
	return nil
}

// Engine.mu is held on entry, and is consumed on every return.
func (e *Engine) refreshMaterializedLocked(scope *StatementScope, caller AuthorizationView, stmt *parser.RefreshMaterializedView, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: REFRESH MATERIALIZED VIEW takes no arguments")
	}
	ref := scope.lockedRef
	if err := view.MaterializedRefreshAccess(caller.View, caller, ref); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	d := caller.Catalog.MustRelation(ref.Address)
	if d != scope.lockedDescriptor || caller.StorageLocked(ref.Address) != scope.lockedStorage {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	concurrent := stmt.Concurrent.Character != 0
	var indices []*catalog.Relation
	var previous storage.Relation
	var oldEntries []storage.IndexEntry
	var ownRows []storage.RowVersion
	var ownDeletes *visibility.PrivateDeletes
	frontier := e.visibility.CommitSeq()
	if concurrent {
		indices = view.MaterializedEligibleIndexes(caller.Catalog, d)
		previous = *scope.lockedStorage
		var root *storage.IndexRoot
		if len(indices) != 0 {
			root = index.EffectiveRoot(e.storage.IndexRoots, scope.owner.Catalog, indices[0].Address())
		}
		if root == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: concurrent materialized index root is missing")
		}
		oldEntries = root.Entries
		ownRows, ownDeletes = scope.columnProjectionRoots(d.Address())
	}
	stage := e.BeginCatalogStage(caller.Catalog)
	stage.authorizeLocked(caller, scope.op.Session)
	stage.querySources = scope.materializedOrZero().Sources
	stage.materialized().SourceView = scope.materializedOrZero().SourceView
	stage.indexes().Output = scope.output
	defer e.DiscardCatalogStage(stage)
	var rows [][]sqltype.Value
	if stmt.NoData {
		e.mu.Unlock()
	} else {
		var err error
		rows, err = e.materializedRefreshRowsLocked(scope, ref, d)
		if err != nil {
			return Result{}, err
		}
	}
	if concurrent {
		var err error
		if len(ownRows) != 0 || ownDeletes != nil {
			previous = view.ConcurrentPrevious(previous, ownRows, ownDeletes, frontier)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: stage.Base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			oldEntries, err = index.BuildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return Result{}, err
			}
		}
		rows, err = view.MaterializedConcurrentRows(scope.op.Context(), caller.Catalog, d, &previous, frontier, oldEntries, indices, rows, scope.output)
		if err != nil {
			return Result{}, err
		}
	}
	if err := e.stageMaterializedRows(scope.op.Context(), stage, d, scope.lockedStorage, rows, !stmt.NoData, scope.txRef); err != nil {
		return Result{}, err
	}
	stage.materialized().Replacement.Concurrent = concurrent
	// Provider validation may replace the catalog. Exact stage fences, not this
	// admission lease, decide whether this original target can still publish.
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

// Population's effective principal and expression identity are restored before
// any stage validation/publication. No session settings or session_user change.
func (e *Engine) materializedRefreshRowsLocked(scope *StatementScope, ref ids.ObjectRef, d *catalog.Relation) ([][]sqltype.Value, error) {
	op := scope.op
	population, owner := operationReplayOrZero(op).materializedPopulation, operationReplayOrZero(op).materializedOwner
	identity, schema := scope.identity, scope.inquiry.currentSchemaResult
	input := scope.input
	operationReplay(op).materializedPopulation, operationReplay(op).materializedOwner = true, d.Owner
	scope.inquiry.currentSchemaResult = sqltype.Value{}
	defer func() {
		operationReplay(op).materializedPopulation, operationReplay(op).materializedOwner = population, owner
		scope.identity, scope.inquiry.currentSchemaResult = identity, schema
		scope.input = input
	}()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		err = scope.captureIdentityLocked(auth)
	}
	if err == nil {
		// Preserve operation settings, but never borrow caller catalog authority
		// for the population owner's restricted reference path.
		candidate := input
		candidate.Regclass, candidate.Regtype, candidate.Regrole = nil, nil, nil
		candidate.Operator, candidate.Regproc, candidate.ACLItem = nil, nil, nil
		candidate.FTS = nil
		candidate.TypeDiagnosticName = nil
		captureEnumOperationSafety(op, &candidate)
		err = stored.CaptureRetainedInput(auth.Catalog, stored.ViewOf(d),
			func() error { return e.captureElementInput(auth, op, &candidate) },
			func(typ *sqltype.Descriptor) error { return e.captureTypeDiagnosticInput(auth, op, &candidate, typ) })
		if err == nil {
			scope.input = candidate
		}
	}
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	root, source, err := e.prepareStoredQueryLocked(scope, ref, stored.ViewOf(d), d.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		return nil, err
	}
	result, _, err := exec.Finalize(op.Context(), root, source, nil)
	if err != nil {
		return nil, err
	}
	return result.(*exec.FinalRows).TakeTableRows(op.Context())
}

func (e *Engine) stageMaterializedRows(ctx context.Context, s *CatalogStage, d *catalog.Relation, previous *storage.Relation, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil || s.materializedOrZero().Replacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.stageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.stageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
		return fmt.Errorf("stuntgres: invalid materialized replacement stage")
	}
	if s.Base.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if d.Revision() == ids.DefinitionRevision(^uint64(0)) || previous.Generation == ^uint64(0) {
		return fmt.Errorf("stuntgres: materialized replacement generation exhausted")
	}
	if err := view.CheckMaterializedRows(d, rows); err != nil {
		return err
	}
	versions, err := e.storage.AllocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	next := *d
	next.ObjectRevision++
	next.Populated = populated
	if err := s.replaceDescriptor(&next, false, false); err != nil {
		return err
	}
	s.materialized().Replacement = &view.MaterializedReplacement{Original: d, Descriptor: &next, Predecessor: previous, Storage: &storage.Relation{Versions: versions, Generation: previous.Generation + 1}, Generation: previous.Generation, Length: len(previous.Versions), Ends: previous.Ends}
	s.materialized().Replacement.IndexCount = view.MaterializedIndexCount(s.Base, d.Address())
	if s.indexesOrZero().Sets == nil {
		s.indexes().Sets = make(map[ids.ObjectAddress]int)
	}
	s.indexes().Sets[d.Address()] = s.materializedOrZero().Replacement.IndexCount
	s.initialTx = tx
	return nil
}

// validateMaterializedReplacement checks the stage's REFRESH replacement.
func (s *CatalogStage) validateMaterializedReplacement() error {
	return view.ValidateMaterializedReplacement(s.materializedOrZero().Replacement, &s.Candidate)
}

// materializedReplacementCurrent checks that the stage's REFRESH replacement
// still applies to snapshot and stored.
func (s *CatalogStage) materializedReplacementCurrent(snapshot *catalog.State, stored *storage.Relation) error {
	return s.materializedOrZero().Replacement.Current(&s.Candidate, snapshot, stored)
}

// materialized returns the statement's materialized slot, allocating it on first use.
func (s *StatementScope) materialized() *view.MaterializedRefreshScope {
	if s.materializedSlot == nil {
		s.materializedSlot = new(view.MaterializedRefreshScope)
	}
	return s.materializedSlot
}

// materializedOrZero reads the materialized slot; a nil slot reads as zero.
func (s *StatementScope) materializedOrZero() view.MaterializedRefreshScope {
	if s.materializedSlot == nil {
		return view.MaterializedRefreshScope{}
	}
	return *s.materializedSlot
}
