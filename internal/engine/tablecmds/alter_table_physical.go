package tablecmds

import (
	"slices"

	"stuntgres/internal/engine/core"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// AlterTablePhysical carries the actual pre-ALTER tuple layout and checked
// plans across the consumed metadata stage. It never owns a mutable catalog.
type AlterTablePhysical struct {
	mutation                                                           core.StatementMutation
	ref                                                                ids.ObjectRef
	old, storageOwner                                                  *catalog.Relation
	store                                                              *storage.Relation
	shape                                                              session.StorageChange
	frontier                                                           ids.CommitSeq
	snapshot                                                           visibility.Root
	snapshotExists                                                     bool
	transformations                                                    []index.ColumnTransformation
	additions                                                          []table.AddedColumn
	storedReplacements, replacements                                   []int32
	typeChecks, addedChecks                                            []catalog.CheckConstraint
	addedIndexes                                                       []ids.ObjectAddress
	validatedChecks                                                    []catalog.CheckConstraint
	validatedNotNull                                                   []catalog.NotNullConstraint
	foreignKeyHistory                                                  []table.AlterForeignKeyScan
	fills                                                              map[int32]sqltype.Value
	rewrite, rewriteEvent, physicalRows, hasAttachments, changedValues bool
	destination                                                        ids.ObjectRef
	validityOnly                                                       bool
}

func finishAlterPhysical(e *core.Engine, scope *core.StatementScope, phase core.AlterMetadataPhase, work *AlterTablePhysical, layouts map[ids.ObjectAddress]index.LayoutLineage) (result core.Result, err error) {
	originalMutation := scope.StatementMutation
	scope.StatementMutation = work.mutation
	scope.LockedDescriptor = phase.View.MustRelation(work.ref.Address)
	defer func() {
		scope.StatementMutation = originalMutation
		if err == nil {
			e.Mu.Lock()
			err = e.AdvanceAlterGrantsLocked(scope, phase.Targets)
			e.Mu.Unlock()
		}
	}()
	auth, stage, err := e.BeginAlterPhysical(scope, phase, work.ref.Address, work.store, work.rewriteEvent)
	if err != nil {
		return core.Result{}, err
	}
	defer e.DiscardCatalogStage(stage)
	core.CarryAlterLayouts(stage, layouts, work.ref.Address)
	op := scope.Op
	ref, old, next := work.ref, work.old, phase.View.MustRelation(work.ref.Address)
	store, storageOwner, shape := work.store, work.storageOwner, work.shape
	var versions []storage.RowVersion
	var ends *storage.DeletionRoot
	if store != nil {
		e.Mu.Lock()
		versions, ends = store.Versions, store.Ends
		e.Mu.Unlock()
	}
	frontier, snapshot, snapshotExists := work.frontier, work.snapshot, work.snapshotExists
	schema := auth.Catalog.Get(next.Namespace()).Name()
	transformations, additions := work.transformations, work.additions
	storedReplacements, replacements := work.storedReplacements, work.replacements
	typeChecks, addedChecks := work.typeChecks, work.addedChecks
	typeChecks, err = table.ResolveAlterChecks(next, typeChecks)
	if err != nil {
		return core.Result{}, err
	}
	addedChecks, err = table.ResolveAlterChecks(next, addedChecks)
	if err != nil {
		return core.Result{}, err
	}
	validatedChecks, err := table.ResolveAlterChecks(next, work.validatedChecks)
	if err != nil {
		return core.Result{}, err
	}
	var validatedAttributes []int32
	for _, selected := range work.validatedNotNull {
		found := slices.ContainsFunc(next.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
			return c.OID == selected.OID && c.Attribute == selected.Attribute && c.Name == selected.Name && !c.NotValid
		})
		if !found {
			return core.Result{}, ids.ErrCatalogReplaced
		}
		validatedAttributes = append(validatedAttributes, selected.Attribute)
	}
	addedIndexes := work.addedIndexes
	fills := work.fills
	rewrite, physicalRows, hasAttachments, changedValues := work.rewrite, work.physicalRows, work.hasAttachments, work.changedValues
	destination := work.destination
	shape.MergeFills(fills, next)
	replacement := store
	projectedWrites, projectedDeletes := scope.ColumnProjectionRoots(ref.Address)
	if work.validityOnly {
		if scanValidator, scan := table.PlanAlterScan(old, next, schema, false, physicalRows, nil, nil, validatedChecks, validatedAttributes, nil, nil); scan {
			if scanValidator.Checks != nil {
				if err := CaptureCheckSnapshots(e, scope, scanValidator.Checks); err != nil {
					return core.Result{}, err
				}
			}
			if err := table.ValidateAddedConstraintRows(AlterRowEvaluation(scope, op, catalog.Read{}), scanValidator, versions, ends, frontier, projectedWrites, projectedDeletes); err != nil {
				return core.Result{}, err
			}
		}
		// Metadata is already private. Validity-only history never installs a
		// heap shape or freezes tuple/index roots through transaction end.
		if scope.Inheritance.Alter == nil {
			if err := validateAlterPhysical(e, scope, phase, work); err != nil {
				return core.Result{}, err
			}
		}
		return core.Result{}, nil
	}
	validator := table.NewAlterRowValidator(next, schema)
	if len(storedReplacements) != 0 {
		typeChecks = table.ValidCheckTraversal(next).Constraints.Check
	}
	if len(typeChecks) != 0 {
		traversal := *next
		traversal.Constraints.Check = typeChecks
		validator.Checks = &traversal
		if err := CaptureCheckSnapshots(e, scope, &traversal); err != nil {
			return core.Result{}, err
		}
	}
	if rewrite {
		// A rewrite creates the destination heap before evaluating old rows.
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return core.Result{}, err
		}
		writes, deletes := scope.ColumnProjectionRoots(storageOwner.Address())
		replacement, projectedWrites, projectedDeletes, err = table.RewriteTypeRows(AlterRowEvaluation(scope, op, scope.RoutineAuth.Catalog), storageOwner, next, store, frontier, writes, deletes, transformations, additions, storedReplacements, validator)
		if err != nil {
			return core.Result{}, err
		}
		versions, ends = replacement.Versions, replacement.Ends
		shape.Rewritten, shape.Frontier = true, frontier
		shape.Snapshot, shape.HasSnapshot = nil, false
	} else if !slices.Equal(storageOwner.Attributes, next.Attributes) {
		shape, projectedWrites, projectedDeletes, err = projectColumnShape(scope, storageOwner, next, store, shape, snapshot, snapshotExists)
		if err != nil {
			return core.Result{}, err
		}
		replacement = shape.Replacement
		versions, ends = replacement.Versions, replacement.Ends
	}
	if physicalRows && !rewrite {
		e.Mu.Lock()
		rows, reownErr := stored.ReownVersions(e.StageRead(stage).State(), replacement.Versions)
		e.Mu.Unlock()
		if reownErr != nil {
			return core.Result{}, reownErr
		}
		if len(rows) != 0 && &rows[0] != &replacement.Versions[0] {
			owned := *replacement
			owned.Versions = rows
			replacement = &owned
		}
		e.Mu.Lock()
		projectedWrites, err = stored.ReownVersions(e.StageRead(stage).State(), projectedWrites)
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		versions = replacement.Versions
	}
	// Recapture effective authority after provider waits and row rewriting.
	// The immutable snapshot is used outside metadata locks for disclosure.
	e.Mu.Lock()
	err = scope.ValidateLockedRelation(e)
	var disclosure core.AuthorizationView
	if err == nil {
		disclosure, err = e.AuthorizationLocked(op)
		disclosure.Catalog = e.PrivateCatalogLocked(scope.PendingCatalog())
		disclosure.Recapture(disclosure.Catalog)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	indexEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: e.StageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	indexBuilds, err := index.ValidateAlterIndexBuilds(&indexEvaluation, disclosure.View, core.DisclosureEnv(&disclosure), e.StageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.Output, transformations, storedReplacements, addedIndexes, rewrite)
	if err != nil {
		return core.Result{}, index.DefaultClassDiagnostic(core.DDLHost{E: e, Op: op, CatalogStage: stage}, disclosure, err)
	}
	var indexOutputRows bool
	projectedWrites, indexOutputRows, err = index.AttachIndexBuildOutputs(op.Context(), projectedWrites, indexBuilds)
	if err != nil {
		return core.Result{}, err
	}
	if scanValidator, scan := table.PlanAlterScan(old, next, schema, rewrite, physicalRows, typeChecks, addedChecks, validatedChecks, validatedAttributes, replacements, validator.NotNull); scan {
		if scanValidator.Checks != nil {
			if err := CaptureCheckSnapshots(e, scope, scanValidator.Checks); err != nil {
				return core.Result{}, err
			}
		}
		if err := table.ValidateAddedConstraintRows(AlterRowEvaluation(scope, scope.Op, catalog.Read{}), scanValidator, versions, ends, frontier, projectedWrites, projectedDeletes); err != nil {
			return core.Result{}, err
		}
	}
	// Without a rewrite, old-row CHECK/NOT NULL validation precedes movement.
	if err := table.ValidateAlterTablespaceMove(destination); err != nil {
		return core.Result{}, err
	}
	shape, record, metadataOnly := table.AlterStorageShape(shape, replacement, old, next, changedValues, len(additions), physicalRows, hasAttachments)
	if err := stage.Replace(next); err != nil {
		return core.Result{}, err
	}
	if record {
		stage.RowShapes = map[ids.ObjectAddress]storage.RowShape{
			ref.Address: {Original: store, Root: store.Versions, Replacement: replacement, ObservedLength: len(store.Versions), ObservedEnds: store.Ends, Generation: store.Generation, OwnProjected: true, Rewritten: rewrite, Frontier: frontier, Fills: shape.Fills},
		}
		stage.AlterRowSources = map[ids.ObjectAddress]*catalog.Relation{ref.Address: storageOwner}
	}
	// Root preparation is pure over produced entries. Its detached collector is
	// never restored as the command journal; only this parent's physical outputs
	// join the fresh successor below.
	changes := scope.PendingCatalog().Clone()
	if physicalRows && !rewrite {
		sourceWrites, _ := scope.ColumnProjectionRoots(ref.Address)
		var transported bool
		projectedWrites, transported, err = core.TransportIndexLayouts(&indexEvaluation, stage, changes, next, store, replacement, sourceWrites, projectedWrites)
		if err != nil {
			return core.Result{}, err
		}
		indexOutputRows = indexOutputRows || transported
	}
	if err := index.PrepareAlterIndexRoots(&indexEvaluation, changes, stage.Base, next, replacement, indexBuilds, metadataOnly); err != nil {
		return core.Result{}, err
	}
	// A shape statement replaces prior own roots only on the successful merge.
	if replacement != store || indexOutputRows {
		scope.StageProjectedColumnRoots(ref.Address, projectedWrites, projectedDeletes)
	}
	if rewrite {
		scope.Owner.Mu.Lock()
		e.Mu.Lock()
		err = scope.ValidateLockedRelation(e)
		if err == nil {
			event := [1]visibility.SerializableWrite{{Rewrite: true}}
			err = e.SerializableWriteLocked(scope, ref, event[:])
		}
		e.Mu.Unlock()
		scope.Owner.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
	}
	for address, candidate := range index.JournalIndexesOrZero(changes).Candidates {
		if candidate.Index.SQLIndex.Parent != ref.Address {
			continue
		}
		if stage.IndexesOrZero().Candidates == nil {
			stage.Indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
		}
		stage.Indexes().Candidates[address] = candidate
		stage.Indexes().Output = scope.Output
	}
	err = e.TransferAlterStage(scope, stage)
	if err != nil {
		return core.Result{}, err
	}
	phase.Record(stage)
	if scope.Inheritance.Alter == nil {
		if err = validateAlterPhysical(e, scope, phase, work); err != nil {
			return core.Result{}, err
		}
	}
	return core.Result{}, nil
}

// Cross-relation checks follow every physical replacement, just as the catalog
// phase precedes every rewrite. They read the genuine latest private heaps.
func validateAlterPhysical(e *core.Engine, scope *core.StatementScope, phase core.AlterMetadataPhase, work *AlterTablePhysical) (err error) {
	original := scope.StatementMutation
	scope.StatementMutation = work.mutation
	defer func() {
		scope.StatementMutation = original
		if err == nil {
			e.Mu.Lock()
			err = e.AdvanceAlterGrantsLocked(scope, phase.Targets)
			e.Mu.Unlock()
		}
	}()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err == nil {
		err = e.AlterPhysicalProofLocked(phase, auth.Catalog, work.ref.Address)
	}
	if err == nil {
		err = e.AdvanceAlterGrantsLocked(scope, phase.Targets)
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	next := auth.Catalog.MustRelation(work.ref.Address)
	writes, deletes := scope.ColumnProjectionRoots(work.ref.Address)
	if err = prepareExclusionShape(scope, work.old, next, auth.Catalog, writes, deletes); err != nil {
		return err
	}
	var prefix []table.ForeignKeyCheck
	if work.changedValues {
		// TYPE/rewrite companions keep their incumbent required traversal.
		prefix = table.ReferencingForeignKeys(auth.Catalog, work.ref.Address)
	}
	checks, err := table.ResolveAlterForeignKeys(next, work.foreignKeyHistory, prefix)
	if err != nil {
		return err
	}
	if len(checks) == 0 {
		return nil
	}
	if err = validateForeignKeys(scope, auth.Catalog, checks); err != nil {
		return err
	}
	return nil
}
