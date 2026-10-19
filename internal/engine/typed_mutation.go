package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type typedColumnAction struct {
	action             parser.AlterTableAction
	usage              *sqltype.Usage
	declaredDimensions int16
}

type typedRelationMutation struct {
	old, next  *catalog.Relation
	transforms []columnTransformation
	additions  []addedColumn
	checks     []catalog.CheckConstraint
}

func (e *Engine) typedMutation(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, old *catalog.Relation, actions []typedColumnAction) (typedRelationMutation, error) {
	mutation := typedRelationMutation{old: old, next: old}
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address())
	raw := make([]parser.AlterTableAction, len(actions))
	for i, action := range actions {
		raw[i] = action.action
	}
	for pass := range 2 {
		for _, planned := range actions {
			a := planned.action
			if !(pass == 0 && a.Action == parser.DropColumn || pass == 1 && a.Action == parser.AddColumn) {
				continue
			}
			var err error
			switch a.Action {
			case parser.DropColumn:
				mutation.next, err = scope.dropColumnCandidate(stage, mutation.next, ref, a, nil)
			case parser.AddColumn:
				var added addedColumn
				mutation.next, added, err = e.addColumnCandidate(scope, op, auth, mutation.next, a, planned.usage)
				if err == nil && added.column != nil {
					if planned.usage != nil {
						mutation.next.Columns[mutation.next.IndexForAttribute(added.attribute)].DeclaredDimensions = planned.declaredDimensions
					}
					mutation.additions = append(mutation.additions, added)
				}
			}
			if err != nil {
				return mutation, err
			}
		}
		if pass == 0 {
			checks, err := captureAlterTypeChecks(op, auth.catalog, old, mutation.next, raw)
			if err != nil {
				return mutation, err
			}
			indexes, err := captureAlterTypeIndexes(scope, auth.catalog, old, mutation.next, raw)
			if err != nil {
				return mutation, err
			}
			// TYPE expressions bind to the original row; the CHECK/index queue
			// binds only after all TYPE candidates have their final usages.
			for _, planned := range actions {
				if planned.action.Action != parser.AlterColumnType {
					continue
				}
				e.mu.Lock()
				prepared, prepareErr := e.prepareAlterTypeLocked(scope, op, auth, old, ref, planned.action, planned.usage)
				e.mu.Unlock()
				if prepareErr != nil {
					return mutation, prepareErr
				}
				if planned.usage != nil {
					prepared.declaredDimensions = planned.declaredDimensions
				}
				var transform columnTransformation
				mutation.next, transform, err = e.alterTypeCandidate(scope, op, auth, stage, old, mutation.next, ref, planned.action, prepared)
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
	next.Defaults = maps.Clone(next.Defaults)
	next.CheckConstraints = slices.Clone(next.CheckConstraints)
	next.NotNullConstraints = slices.Clone(next.NotNullConstraints)
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
		if err := rowRewriteError(auth.catalog, old); err != nil {
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
	store := e.storage.relations[old.Address()]
	frontier := e.commitSeq
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
		rewrite = rewrite || transform.rewrite
	}
	if perRowAdd {
		if err := rowRewriteError(auth.catalog, old); err != nil {
			return err
		}
	}
	if rewrite {
		scope.owner.mu.Lock()
		err = scope.acquireSerializableSnapshot()
		if err == nil {
			e.mu.Lock()
			err = e.serializableReadLocked(scope, e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address()))
			e.mu.Unlock()
		}
		scope.owner.mu.Unlock()
		if err != nil {
			return err
		}
	}
	schema := auth.catalog.Get(old.Namespace()).Name()
	validator := newAlterRowValidator(next, schema)
	if len(mutation.checks) != 0 {
		checks := *next
		checks.CheckConstraints = mutation.checks
		validator.checks = validCheckTraversal(&checks)
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
		versions, projectionErr := projection.versions(scope.op.Context(), store.versions)
		if projectionErr != nil {
			return projectionErr
		}
		if store.generation == ^uint64(0) {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		replacement = &relationStorage{versions: versions, ends: store.ends, generation: store.generation + 1}
	}
	if err != nil {
		return err
	}
	if !rewrite && len(mutation.checks) != 0 {
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
		for _, row := range replacement.versions {
			if err := scope.op.Context().Err(); err != nil {
				return err
			}
			if !liveVersion(row, frontier, replacement.ends, nil) {
				continue
			}
			if err := validator.validate(row.values, &evaluation); err != nil {
				return err
			}
		}
	}
	e.mu.Lock()
	next, err = e.rebuildAlterUniquesLocked(stage, next, mutation.transforms)
	if err == nil {
		next, err = e.rebuildAlterExclusionsLocked(stage, next, mutation.transforms)
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
		indexEvaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: e.stageRead(stage)}
		builds, err := validateAlterIndexBuilds(&indexEvaluation, auth, e.stageRead(stage), next, replacement, nil, nil, schema, scope.output, mutation.transforms, nil, nil, rewrite)
		if err != nil {
			return err
		}
		for _, build := range builds {
			if stage.indexCandidates == nil {
				stage.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate)
			}
			stage.indexCandidates[build.index.Address()] = indexRootCandidate{index: build.index, root: build.root, storage: indexStorageIdentity(replacement), generation: replacement.generation, ends: replacement.ends, length: len(replacement.versions)}
		}
	}
	if replacement != store {
		if stage.rowShapes == nil {
			stage.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
		}
		stage.rowShapes[old.Address()] = catalogRowShape{original: store, replacement: replacement, observedLength: len(store.versions), observedEnds: store.ends, generation: store.generation}
	}
	if !rewrite && len(stage.indexLayouts) != 0 {
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: e.stageRead(stage)}
		transported := newCatalogChanges()
		if _, _, err := transportIndexLayouts(&evaluation, stage, transported, next, store, replacement, nil, nil); err != nil {
			return err
		}
		for address, candidate := range transported.indexCandidates {
			if stage.indexCandidates == nil {
				stage.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate)
			}
			stage.indexCandidates[address] = candidate
		}
	}
	return nil
}
