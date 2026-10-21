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
	"stuntgres/internal/engine/visibility"
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
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address())
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
				e.mu.Lock()
				prepared, prepareErr := e.prepareAlterTypeLocked(scope, op, auth, old, ref, planned.Action, planned.Usage)
				e.mu.Unlock()
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
	e.mu.Lock()
	err := e.assignDefinitionOIDsLocked(&next)
	e.mu.Unlock()
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
	e.mu.Lock()
	store := e.storage.Relations[old.Address()]
	frontier := e.visibility.CommitSeq()
	e.mu.Unlock()
	if store == nil {
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	fills, perRowAdd, err := planAddedColumns(scope, mutation.additions)
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
		scope.owner.Mu.Lock()
		err = scope.acquireSerializableSnapshot()
		if err == nil {
			e.mu.Lock()
			err = e.serializableReadLocked(scope, e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address()))
			e.mu.Unlock()
		}
		scope.owner.Mu.Unlock()
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
		e.mu.Lock()
		err = e.captureCheckCommentSnapshotLocked(scope, &checks)
		if err == nil {
			err = e.captureCheckVisibilitySnapshotLocked(scope, &checks)
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if rewrite {
		replacement, _, _, err = rewriteTypeRows(scope, scope.op, old, next, store, frontier, mutation.transforms, mutation.additions, nil, validator)
	} else if !slices.Equal(old.Attributes, next.Attributes) {
		projection, projectionErr := newColumnProjection(old, next, fills)
		if projectionErr != nil {
			return projectionErr
		}
		versions, projectionErr := projection.versions(scope.op.Context(), store.Versions)
		if projectionErr != nil {
			return projectionErr
		}
		if store.Generation == ^uint64(0) {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		replacement = &storage.Relation{Versions: versions, Ends: store.Ends, Generation: store.Generation + 1}
	}
	if err != nil {
		return err
	}
	if !rewrite && len(mutation.checks) != 0 {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		for _, row := range replacement.Versions {
			if err := scope.op.Context().Err(); err != nil {
				return err
			}
			if !visibility.LiveVersion(row, frontier, replacement.Ends, nil) {
				continue
			}
			if err := validator.Validate(row.Values, &evaluation); err != nil {
				return err
			}
		}
	}
	e.mu.Lock()
	h := ddlHost{e: e, op: scope.op, stage: stage}
	next, err = RebuildAlterUniques(h, next, mutation.transforms)
	if err == nil {
		next, err = e.rebuildAlterNotNullsLocked(stage, next, mutation.transforms)
	}
	if err == nil {
		next, err = RebuildAlterExclusions(h, next, mutation.transforms)
	}
	if err == nil {
		err = e.evolveStageIndexesLocked(stage, old.Address(), mutation.transforms)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := stage.Replace(next); err != nil {
		return err
	}
	if rewrite || len(mutation.transforms) != 0 {
		indexEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: e.stageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		builds, err := index.ValidateAlterIndexBuilds(&indexEvaluation, auth.View, disclosureEnv(&auth), e.stageRead(stage), next, replacement, nil, nil, schema, scope.output, mutation.transforms, nil, nil, rewrite)
		if err != nil {
			return err
		}
		for _, build := range builds {
			if stage.indexesOrZero().Candidates == nil {
				stage.indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
			}
			stage.indexes().Candidates[build.Index.Address()] = index.RootCandidate{Index: build.Index, Root: build.Root, Storage: replacement.IndexIdentity(), Generation: replacement.Generation, Ends: replacement.Ends, Length: len(replacement.Versions)}
		}
	}
	if replacement != store {
		if stage.rowShapes == nil {
			stage.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
		}
		stage.rowShapes[old.Address()] = catalogRowShape{original: store, replacement: replacement, observedLength: len(store.Versions), observedEnds: store.Ends, generation: store.Generation}
	}
	if !rewrite && len(stage.indexesOrZero().Layouts) != 0 {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: e.stageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		transported := session.NewJournal()
		if _, _, err := transportIndexLayouts(&evaluation, stage, transported, next, store, replacement, nil, nil); err != nil {
			return err
		}
		for address, candidate := range index.JournalIndexesOrZero(transported).Candidates {
			if stage.indexesOrZero().Candidates == nil {
				stage.indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
			}
			stage.indexes().Candidates[address] = candidate
		}
	}
	return nil
}
