package viewcmds

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RefreshMaterializedLocked runs REFRESH MATERIALIZED VIEW, ordinary or
// CONCURRENTLY, WITH DATA or WITH NO DATA.
//
// Engine.mu is held on entry, and is consumed on every return.
func RefreshMaterializedLocked(e *core.Engine, scope *core.StatementScope, caller core.AuthorizationView, stmt *parser.RefreshMaterializedView, count int) (core.Result, error) {
	if count != 0 {
		e.Mu.Unlock()
		return core.Result{}, fmt.Errorf("stuntgres: REFRESH MATERIALIZED VIEW takes no arguments")
	}
	ref := scope.LockedRef
	if err := view.MaterializedRefreshAccess(caller.View, caller, ref); err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	d := caller.Catalog.MustRelation(ref.Address)
	if d != scope.LockedDescriptor || caller.StorageLocked(ref.Address) != scope.LockedStorage {
		e.Mu.Unlock()
		return core.Result{}, ids.ErrCatalogReplaced
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
			root = index.EffectiveRoot(e.Storage.IndexRoots, scope.PendingCatalog(), indices[0].Address())
		}
		if root == nil {
			e.Mu.Unlock()
			return core.Result{}, fmt.Errorf("stuntgres: concurrent materialized index root is missing")
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
			return core.Result{}, err
		}
	}
	if concurrent {
		var err error
		if len(ownRows) != 0 || ownDeletes != nil {
			previous = view.ConcurrentPrevious(previous, ownRows, ownDeletes, frontier)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: stage.Base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			oldEntries, err = index.BuildIndexEntries(&evaluation, d, indices[0].SQLIndex, &previous, nil)
			if err != nil {
				return core.Result{}, err
			}
		}
		rows, err = view.MaterializedConcurrentRows(scope.Op.Context(), caller.Catalog, d, &previous, frontier, oldEntries, indices, rows, scope.Output)
		if err != nil {
			return core.Result{}, err
		}
	}
	if err := stageMaterializedRows(e, scope.Op.Context(), stage, d, scope.LockedStorage, rows, !stmt.NoData, scope.TxRef); err != nil {
		return core.Result{}, err
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
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.CompleteUtility()
	}
	return core.Result{}, err
}

// Population's effective principal and expression identity are restored before
// any stage validation/publication. No session settings or session_user change.
func materializedRefreshRowsLocked(e *core.Engine, scope *core.StatementScope, ref ids.ObjectRef, d *catalog.Relation) ([][]sqltype.Value, error) {
	op := scope.Op
	population, owner := core.OperationReplayOrZero(op).MaterializedPopulation, core.OperationReplayOrZero(op).MaterializedOwner
	identity, schema := scope.Identity, scope.Inquiry.CurrentSchemaResult
	input := scope.Input
	core.OperationReplay(op).MaterializedPopulation, core.OperationReplay(op).MaterializedOwner = true, d.Owner
	scope.Inquiry.CurrentSchemaResult = sqltype.Value{}
	defer func() {
		core.OperationReplay(op).MaterializedPopulation, core.OperationReplay(op).MaterializedOwner = population, owner
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
		core.CaptureEnumOperationSafety(op, &candidate)
		err = stored.CaptureRetainedInput(auth.Catalog, stored.ViewOf(d),
			func() error { return e.CaptureElementInput(auth, op, &candidate) },
			func() error {
				if candidate.RegTS != nil {
					return nil
				}
				var err error
				candidate.RegTS, err = core.CaptureRegTSInput(auth)
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

func stageMaterializedRows(e *core.Engine, ctx context.Context, s *core.CatalogStage, d *catalog.Relation, previous *storage.Relation, rows [][]sqltype.Value, populated bool, tx ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if s == nil || s.Owner != e || s.Lifecycle != core.CatalogOpen || s.Invalid != nil || s.MaterializedOrZero().Replacement != nil || d == nil || d.RelationKind != catalog.MaterializedViewRelation || d.View == nil || s.Base.Get(d.Address()) != d || e.StageCatalogBaseLocked(s).Get(d.Address()) != d || previous == nil || e.StageStorageLocked(s, d.Address()) != previous || !populated && len(rows) != 0 {
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

// materializedStage returns the materialized slot, allocating it on first use.
func materializedStage(s *core.CatalogStage) *view.MaterializedStage {
	if s.MaterializedSlot == nil {
		s.MaterializedSlot = new(view.MaterializedStage)
	}
	return s.MaterializedSlot
}
