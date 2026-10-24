package engine

import (
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
	e.mu.Lock()
	if scope.txRef.Epoch() != e.catalog.State.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	captures, err := index.SelectOverlayCapturesLocked(&e.storage, view, &stage.Candidate, stage.indexesOrZero(), scope.owner.Catalog, c)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for i := range captures {
		capture := &captures[i]
		idx, parent := capture.Index, capture.Parent
		building := true
		indexAuth := auth
		if r := stage.materializedOrZero().Replacement; r != nil && r.Concurrent && r.Original.Address() == parent.Address() {
			building = false
			indexAuth.State.CurrentUser = parent.Owner
			if _, err := indexAuth.Roles.Descriptor(parent.Owner); err != nil {
				return err
			}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.RoutineAuthority = &auth
		if idx.SQLIndex.HasRuntimeDomainConversion() {
			var err error
			evaluation.Input.TypeDiagnosticName, err = captureTypeDiagnosticName(indexAuth)
			if err != nil {
				return err
			}
		}
		writes, deletes := scope.columnProjectionRoots(parent.Address())
		if stage.initialTables[parent.Address()] != nil {
			writes = scope.stage.rows[parent.Address()]
		}
		visible, err := index.BuildVisibleStore(scope.op.Context(), &capture.Store, writes, deletes)
		if err != nil {
			return err
		}
		var outputs *index.BuildCapture
		if idx.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 {
			outputs = &index.BuildCapture{Index: idx}
		}
		if outputs != nil {
			outputs.Outputs = make([]storage.IndexOutput, len(visible.Versions))
		}
		full, err := index.BuildIndexRoot(&evaluation, parent, idx.SQLIndex, visible, outputs)
		if err != nil {
			return err
		}
		schema := view.Get(parent.Namespace()).Name()
		if err := index.ValidateUniqueRoot(scope.op.Context(), parent, idx, full, schema, scope.output, &indexAuth.View, disclosureEnv(&indexAuth), building); err != nil {
			return err
		}
		build := &index.AlterIndexBuild{Index: idx, Root: full, Store: visible}
		if outputs != nil {
			build.Outputs = outputs.Outputs
		}
		var positions map[index.RowIdentity]int
		if visible != &capture.Store {
			positions, err = index.BuildPositions(scope.op.Context(), &capture.Store)
			if err != nil {
				return err
			}
		}
		baseline, err := build.BaselineRoot(scope.op.Context(), idx, &capture.Store, positions)
		if err != nil {
			return err
		}
		index.RetainIndexBuild(c, idx, &capture.Store, baseline)
		updated, changed, err := index.AttachIndexBuildOutputs(scope.op.Context(), writes, map[string]*index.AlterIndexBuild{idx.Name(): build})
		if err != nil {
			return err
		}
		if changed {
			if stage.initialTables[parent.Address()] != nil {
				// These rows were produced by this CREATE statement, not an
				// earlier transaction prefix owned by the projection helper.
				scope.stage.rows[parent.Address()] = updated
			} else {
				scope.stageProjectedColumnRoots(parent.Address(), updated, deletes)
			}
		}
	}
	return scope.op.Context().Err()
}
