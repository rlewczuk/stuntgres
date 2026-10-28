package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Ordinary key providers run only on detached storage captures, never under
// publication locks. The committed baseline remains distinct from own rows;
// reconstructing the overlay validates CREATE/REINDEX at this statement, with
// build diagnostics, rather than incorrectly delaying errors until COMMIT.
func (e *Engine) prepareCatalogOverlayIndexes(scope *StatementScope, stage *CatalogStage, c *session.Journal) error {
	e.Mu.Lock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.PrivateCatalogLocked(c)
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	captures, err := index.SelectOverlayCapturesLocked(&e.Storage, view, &stage.Candidate, stage.IndexesOrZero(), scope.PendingCatalog(), c)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	for i := range captures {
		capture := &captures[i]
		idx, parent := capture.Index, capture.Parent
		if stage.deferredAlterRows[parent.Address()] {
			continue
		}
		building := true
		indexAuth := auth
		if r := stage.MaterializedOrZero().Replacement; r != nil && r.Concurrent && r.Original.Address() == parent.Address() {
			building = false
			indexAuth.State.CurrentUser = parent.Owner
			if _, err := indexAuth.Roles.Descriptor(parent.Owner); err != nil {
				return err
			}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.RoutineAuthority = &auth
		if idx.SQLIndex.HasRuntimeDomainConversion() {
			var err error
			evaluation.Input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(indexAuth)
			if err != nil {
				return err
			}
		}
		writes, deletes := scope.ColumnProjectionRoots(parent.Address())
		if stage.initialTables[parent.Address()] != nil {
			writes = scope.Stage.Rows[parent.Address()]
		}
		visible, err := index.BuildVisibleStore(scope.Op.Context(), &capture.Store, writes, deletes)
		if err != nil {
			return err
		}
		var outputs *index.BuildCapture
		if idx.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 || idx.SQLIndex.Method == catalog.BrinMethod {
			outputs = &index.BuildCapture{Index: idx}
		}
		if outputs != nil {
			outputs.Outputs = make([]storage.IndexOutput, len(visible.Versions))
		}
		if observer := stage.IndexesOrZero().Progress.ObserverFor(idx.Address()); observer != nil {
			if outputs == nil {
				outputs = &index.BuildCapture{Progress: observer}
			} else {
				outputs.Progress = observer
			}
		}
		full, err := index.BuildRelationIndexRoot(&evaluation, parent, idx, visible, outputs)
		if err != nil {
			return err
		}
		schema := view.Get(parent.Namespace()).Name()
		if err := index.ValidateUniqueRoot(scope.Op.Context(), parent, idx, full, schema, scope.Output, &indexAuth.View, DisclosureEnv(&indexAuth), building); err != nil {
			return err
		}
		stage.IndexesOrZero().Progress.Complete(idx.Address())
		build := &index.AlterIndexBuild{Index: idx, Root: full, Store: visible}
		if outputs != nil {
			build.Outputs = outputs.Outputs
		}
		var positions map[index.RowIdentity]int
		if visible != &capture.Store {
			positions, err = index.BuildPositions(scope.Op.Context(), &capture.Store)
			if err != nil {
				return err
			}
		}
		baseline, err := build.BaselineRoot(&evaluation, idx, &capture.Store, positions)
		if err != nil {
			return err
		}
		index.RetainIndexBuild(c, idx, &capture.Store, baseline)
		updated, changed, err := index.AttachIndexBuildOutputs(scope.Op.Context(), writes, map[string]*index.AlterIndexBuild{idx.Name(): build})
		if err != nil {
			return err
		}
		if changed {
			if stage.initialTables[parent.Address()] != nil {
				// These rows were produced by this CREATE statement, not an
				// earlier transaction prefix owned by the projection helper.
				scope.Stage.Rows[parent.Address()] = updated
			} else {
				scope.StageProjectedColumnRoots(parent.Address(), updated, deletes)
			}
		}
	}
	return scope.Op.Context().Err()
}
