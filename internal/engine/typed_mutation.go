package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

type typedRelationMutation struct {
	old, next  *catalog.Relation
	transforms []index.ColumnTransformation
	additions  []table.AddedColumn
	checks     []catalog.CheckConstraint
}

func (e *Engine) typedMutation(scope *StatementScope, op *session.Operation, auth AuthorizationView, stage *CatalogStage, old *catalog.Relation, actions []typedef.TypedColumnAction) (typedRelationMutation, error) {
	mutation := typedRelationMutation{old: old, next: old}
	ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), old.Address())
	raw := make([]parser.AlterTableAction, len(actions))
	for i, action := range actions {
		raw[i] = action.Action
	}
	for pass := range 2 {
		for _, planned := range actions {
			a := planned.Action
			if !(pass == 0 && a.Action == parser.DropColumn || pass == 1 && a.Action == parser.AddColumn) {
				continue
			}
			var err error
			switch a.Action {
			case parser.DropColumn:
				mutation.next, err = scope.dropColumnCandidate(stage, mutation.next, ref, a, nil)
			case parser.AddColumn:
				var added table.AddedColumn
				mutation.next, added, err = e.addColumnCandidate(scope, op, auth, mutation.next, a, planned.Usage)
				if err == nil && added.Column != nil {
					if planned.Usage != nil {
						mutation.next.Columns[mutation.next.IndexForAttribute(added.Attribute)].DeclaredDimensions = planned.DeclaredDimensions
					}
					mutation.additions = append(mutation.additions, added)
				}
			}
			if err != nil {
				return mutation, err
			}
		}
		if pass == 0 {
			checks, err := table.CaptureAlterTypeChecks(op, auth.Catalog, old, mutation.next, raw)
			if err != nil {
				return mutation, err
			}
			indexes, err := captureAlterTypeIndexes(scope, auth.Catalog, old, mutation.next, raw)
			if err != nil {
				return mutation, err
			}
			// TYPE expressions bind to the original row; the CHECK/index queue
			// binds only after all TYPE candidates have their final usages.
			for _, planned := range actions {
				if planned.Action.Action != parser.AlterColumnType {
					continue
				}
				e.Mu.Lock()
				prepared, prepareErr := e.prepareAlterTypeLocked(scope, op, auth, old, ref, planned.Action, planned.Usage)
				e.Mu.Unlock()
				if prepareErr != nil {
					return mutation, prepareErr
				}
				if planned.Usage != nil {
					prepared.DeclaredDimensions = planned.DeclaredDimensions
				}
				var transform index.ColumnTransformation
				mutation.next, transform, err = e.alterTypeCandidate(scope, op, auth, stage, old, mutation.next, ref, planned.Action, prepared)
				if err != nil {
					return mutation, err
				}
				if old.RelationKind != catalog.CompositeRelation {
					mutation.transforms = append(mutation.transforms, transform)
				}
			}
			mutation.next, mutation.checks, err = e.rebindAlterTypeChecks(scope, op, auth, ref, mutation.next, checks)
			if err == nil {
				err = e.rebindAlterTypeIndexes(scope, auth, mutation.next, stage, indexes)
			}
			if err != nil {
				return mutation, err
			}
		}
	}
	if mutation.next == old {
		return mutation, nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return mutation, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *mutation.next
	next.ObjectRevision = old.Revision() + 1
	next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
	next.Constraints.Check = slices.Clone(next.Constraints.Check)
	next.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
	e.Mu.Lock()
	err := table.AssignDefinitionOIDs(DDLHost{E: e}, &next)
	e.Mu.Unlock()
	mutation.next = &next
	return mutation, err
}

func (e *Engine) stageTypedRows(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, mutation typedRelationMutation) error {
	old, next := mutation.old, mutation.next
	if old.RelationKind != catalog.TableRelation {
		return nil
	}
	// An explicit TYPE action protects stored own-row consumers even when the
	// candidate preserves every Usage and descriptor pointer. Keep semantic
	// admission ahead of the no-physical-change fast path.
	if len(mutation.transforms) != 0 {
		if err := typedef.RowRewriteError(auth.Catalog, old); err != nil {
			return err
		}
	}
	if old == next {
		return nil
	}
	if len(mutation.transforms) == 0 && slices.Equal(old.Attributes, next.Attributes) {
		return nil
	}
	e.Mu.Lock()
	store := e.StageStorageLocked(stage, old.Address())
	frontier := e.Visibility.CommitSeq()
	e.Mu.Unlock()
	if store == nil {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	fills, perRowAdd, err := table.PlanAddedColumns(alterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), mutation.additions)
	if err != nil {
		return err
	}
	replacement := store
	rewrite := perRowAdd
	for _, transform := range mutation.transforms {
		rewrite = rewrite || transform.Rewrite
	}
	if perRowAdd {
		if err := typedef.RowRewriteError(auth.Catalog, old); err != nil {
			return err
		}
	}
	if rewrite {
		scope.Owner.Mu.Lock()
		err = scope.AcquireSerializableSnapshot()
		if err == nil {
			e.Mu.Lock()
			err = e.SerializableReadLocked(scope, e.Catalog.Domain.Ref(stage.Base.Epoch(), old.Address()))
			e.Mu.Unlock()
		}
		scope.Owner.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	schema := auth.Catalog.Get(old.Namespace()).Name()
	validator := table.NewAlterRowValidator(next, schema)
	if len(mutation.checks) != 0 {
		checks := *next
		checks.Constraints.Check = mutation.checks
		validator.Checks = table.ValidCheckTraversal(&checks)
		if err := e.captureCheckSnapshots(scope, &checks); err != nil {
			return err
		}
	}
	sourceWrites, projectedDeletes := scope.ColumnProjectionRoots(old.Address())
	projectedWrites := sourceWrites
	if rewrite {
		replacement, projectedWrites, projectedDeletes, err = table.RewriteTypeRows(alterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), old, next, store, frontier, sourceWrites, projectedDeletes, mutation.transforms, mutation.additions, nil, validator)
	} else if !slices.Equal(old.Attributes, next.Attributes) {
		projection, projectionErr := session.NewColumnProjection(old, next, fills)
		if projectionErr != nil {
			return projectionErr
		}
		versions, projectionErr := projection.Versions(scope.Op.Context(), store.Versions)
		if projectionErr != nil {
			return projectionErr
		}
		if store.Generation == ^uint64(0) {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		replacement = &storage.Relation{Versions: versions, Ends: store.Ends, Generation: store.Generation + 1}
		projectedWrites, err = projection.Versions(scope.Op.Context(), sourceWrites)
		projectedDeletes = session.ProjectDeletions(projectedDeletes, replacement)
	}
	if err != nil {
		return err
	}
	if !rewrite && len(mutation.checks) != 0 {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if err := table.ValidateAddedConstraintRows(evaluation, validator, replacement.Versions, replacement.Ends, frontier, projectedWrites, projectedDeletes); err != nil {
			return err
		}
	}
	e.Mu.Lock()
	h := TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}
	next, err = index.RebuildAlterUniques(h, next, mutation.transforms)
	if err == nil {
		next, err = table.RebuildAlterNotNulls(DDLHost{E: e, CatalogStage: stage}, next, mutation.transforms)
	}
	if err == nil {
		next, err = index.RebuildAlterExclusions(h, next, mutation.transforms)
	}
	if err == nil {
		err = e.EvolveStageIndexesLocked(stage, old.Address(), mutation.transforms)
	}
	e.Mu.Unlock()
	if err != nil {
		return index.DefaultClassDiagnostic(h, auth, err)
	}
	if err := stage.Replace(next); err != nil {
		return err
	}
	if rewrite || len(mutation.transforms) != 0 {
		indexEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: e.StageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		builds, err := index.ValidateAlterIndexBuilds(&indexEvaluation, auth.View, DisclosureEnv(&auth), e.StageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.Output, mutation.transforms, nil, nil, rewrite)
		if err != nil {
			return index.DefaultClassDiagnostic(h, auth, err)
		}
		projectedWrites, _, err = index.AttachIndexBuildOutputs(scope.Op.Context(), projectedWrites, builds)
		if err != nil {
			return err
		}
		var positions map[index.RowIdentity]int
		for _, build := range builds {
			if build.Store != replacement {
				positions, err = index.BuildPositions(scope.Op.Context(), replacement)
				if err != nil {
					return err
				}
				break
			}
		}
		for _, build := range builds {
			if stage.IndexesOrZero().Candidates == nil {
				stage.Indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
			}
			root, err := build.BaselineRoot(scope.Op.Context(), build.Index, replacement, positions)
			if err != nil {
				return err
			}
			stage.Indexes().Candidates[build.Index.Address()] = index.RootCandidate{Index: build.Index, Root: root, Storage: replacement.IndexIdentity(), Generation: replacement.Generation, Ends: replacement.Ends, Length: len(replacement.Versions)}
		}
	}
	if replacement != store {
		if stage.RowShapes == nil {
			stage.RowShapes = make(map[ids.ObjectAddress]storage.RowShape)
		}
		stage.RowShapes[old.Address()] = storage.RowShape{Original: store, Root: store.Versions, Replacement: replacement, ObservedLength: len(store.Versions), ObservedEnds: store.Ends, Generation: store.Generation, OwnProjected: true, Rewritten: rewrite, Frontier: frontier, Fills: fills}
	}
	if !rewrite && len(stage.IndexesOrZero().Layouts) != 0 {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: e.StageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		transported := scope.Owner.Catalog.Clone()
		projectedWrites, _, err = TransportIndexLayouts(&evaluation, stage, transported, next, store, replacement, sourceWrites, projectedWrites)
		if err != nil {
			return err
		}
		for address, candidate := range index.JournalIndexesOrZero(transported).Candidates {
			if _, changed := stage.IndexesOrZero().Layouts[address]; !changed || candidate.Index.SQLIndex.Parent != old.Address() {
				continue
			}
			if stage.IndexesOrZero().Candidates == nil {
				stage.Indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
			}
			stage.Indexes().Candidates[address] = candidate
		}
	}
	if replacement != store || len(mutation.transforms) != 0 {
		scope.StageProjectedColumnRoots(old.Address(), projectedWrites, projectedDeletes)
	}
	return nil
}
