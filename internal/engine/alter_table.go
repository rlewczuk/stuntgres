package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// The strictest captured ALTER grant protects the incarnation until Finish.
// The caller holds Engine.mu. Every return releases it before callbacks/scanning.
func (e *Engine) alterTableLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTable, count int) (Result, error) {
	scope.RoutineAuth = auth
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.ResolveRelationReference(auth, s.Name, true)
	if s.IfExists && table.RenameMissing(err) {
		return e.skipMissingRelationLocked(scope, op, s.Name.Parts[len(s.Name.Parts)-1].Text, false)
	}
	if err == nil && ref != scope.LockedRef {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.LockedDescriptor.(*catalog.Relation)
	if s.Only && scope.Inheritance.Alter == nil && !catalog.HierarchyRelation(old) {
		s = table.OnlySetNotNull(auth.Catalog, old, s)
	}
	if PartitionUniqueReplayAllowed(op, auth, s, old) {
		return e.executePartitionUniqueReplayLocked(scope, op, auth, s, old)
	}
	if catalog.HierarchyRelation(old) && partition.ExclusionAddActions(s) {
		return e.executePartitionExclusionAddLocked(scope, op, auth, s, old)
	}
	if catalog.HierarchyRelation(old) && index.PartitionConstraintDropActions(old, s.Actions) {
		return e.executePartitionConstraintDropLocked(scope, auth, old, s)
	}
	if len(s.Actions) == 0 {
		return fail(fmt.Errorf("stuntgres: empty ALTER TABLE"))
	}
	if err := typedef.TypedDirectActionsGuard(old, s.Actions); err != nil {
		return fail(err)
	}
	var preparation table.AlterTablePreparation
	if scope.Inheritance.Alter != nil && !scope.Inheritance.Alter.Descendant {
		preparation = table.AlterTablePreparation{Actions: s.Actions, Destination: scope.Inheritance.Alter.Tablespace, Types: scope.Inheritance.Alter.Types}
	} else {
		preparation, err = e.prepareAlterTableActions(scope, op, auth, old, ref, s.Actions)
		if err != nil {
			if len(preparation.Types) == 0 {
				return fail(err)
			}
			// A completed admission followed by a coercion failure still runs
			// the real sequence-AS prephase. Its private stage is discarded,
			// while the caller's already-discarded cache tail stays discarded.
			e.Mu.Unlock()
			stage, stageErr := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, preparation.Types)
			if scope.Inheritance.Alter == nil {
				e.DiscardCatalogStage(stage)
			}
			if stageErr != nil {
				return Result{}, stageErr
			}
			return Result{}, err
		}
	}
	work, destination, prepared := preparation.Actions, preparation.Destination, preparation.Types
	if len(work) == 0 {
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
		e.Mu.Unlock()
		return Result{}, nil
	}
	if len(work) != len(s.Actions) {
		command := *s
		command.Actions = work
		s = &command
	}
	if scope.Inheritance.Alter == nil && !access.RowSecurityActions(s.Actions) && !table.RelationOptionsOnly(s.Actions) && (catalog.InheritanceParticipant(auth.Catalog, old) || table.HasRelationshipActions(s.Actions) || catalog.HierarchyRelation(old) && table.PartitionConstraintActions(old, s.Actions)) {
		return e.alterInheritanceLocked(scope, op, auth, s, count, destination, prepared)
	}
	if scope.Inheritance.Alter == nil || !scope.Inheritance.Alter.Descendant {
		if err := table.InheritanceAlterActionsGuard(auth.Catalog, old, s.Actions); err != nil {
			return fail(err)
		}
	}
	if err := table.VerifyAlterPrimaryPrerequisites(old, s.Actions); err != nil {
		return fail(err)
	}
	if access.RowSecurityActions(s.Actions) {
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		return e.stageRowSecurityFlagsLocked(scope, op, auth, old, access.RowSecurityFlags(old, s.Actions))
	}
	if table.RelationOptionsOnly(s.Actions) {
		next, err := table.RelationOptionsOnlyCandidate(old, s.Actions, destination)
		if err != nil {
			return fail(err)
		}
		return e.stageRowSecurityFlagsLocked(scope, op, auth, old, next)
	}
	changes := scope.Owner.Catalog.Clone()
	if scope.Inheritance.Alter != nil {
		changes = scope.Inheritance.Alter.Changes
	}
	relationshipOnly := table.RelationshipOnly(s.Actions)
	// Virtual partition nodes share the candidate/provider/publication pipeline,
	// but have no heap. The inheritance plan visits physical descendants with
	// their own storage, validators and journal fences.
	physicalRows := old.Partition == nil
	var original, store *storage.Relation
	var shape session.StorageChange
	var versions []storage.RowVersion
	var ends *storage.DeletionRoot
	var frontier ids.CommitSeq
	var snapshot visibility.Root
	var snapshotExists bool
	grantCatalog := e.Catalog.State
	schema := auth.Catalog.Get(old.Namespace()).Name()
	if !relationshipOnly && physicalRows {
		original = e.Storage.Relations[ref.Address]
		store = e.PrivateStorageLocked(changes, ref.Address)
		if store == nil {
			return fail(fmt.Errorf("stuntgres: relation storage is missing"))
		}
		exists := false
		if changes != nil {
			shape, exists = changes.Storage[ref.Address]
		}
		if !exists {
			shape = session.StorageChange{Original: original, Root: original.Versions, Ends: original.Ends, Generation: original.Generation, Replacement: store, Relation: old}
		}
		versions, ends, frontier = store.Versions, store.Ends, e.Visibility.CommitSeq()
		snapshot, snapshotExists = scope.Owner.Roots[ref.Address]
	}
	var stage *CatalogStage
	if scope.Inheritance.Alter == nil || !scope.Inheritance.Alter.Descendant {
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
	}
	e.Mu.Unlock()
	defer func() {
		if scope.Inheritance.Alter == nil {
			e.DiscardCatalogStage(stage)
		}
	}()
	originalDefinition := op.Definition
	if scope.Inheritance.Alter != nil {
		op.Definition = StageDefinitions(scope.Inheritance.Alter.Stage)
		auth.Definition = op.Definition
	}
	defer func() { op.Definition = originalDefinition }()
	preparedStage, err := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, prepared)
	stage = preparedStage
	if stage == nil && scope.Inheritance.Alter != nil {
		stage = scope.Inheritance.Alter.Stage
	}
	if err != nil {
		return Result{}, err
	}
	next := old
	definition := scope.inheritancePreparedNode()
	var transformations []index.ColumnTransformation
	var pendingChecks []table.AlterTypeCheck
	var pendingIndexes []table.AlterTypeIndex
	var typeChecks []catalog.CheckConstraint
	var addedChecks []catalog.CheckConstraint
	var addedIndexes []ids.ObjectAddress
	var addedForeignKeys []ids.OID
	var additions []table.AddedColumn
	var replacements []int32
	var attachments []index.UsingIndexAttachment
	hasAttachments := false
	hasForeignKeys := len(scope.foreignKeysOrZero().AlterKeys) != 0
	for _, action := range s.Actions {
		hasAttachments = hasAttachments || action.Action == parser.AddUsingIndex
	}
	// Keep shape/generated/identity phases. Index metadata precedes other
	// constraint bindings; historical row checks run only after index builds.
	for pass := range 10 {
		typeIndex := 0
		attachmentIndex := 0
		for actionIndex, action := range s.Actions {
			selected := table.AlterActionPhase(action.Action) == pass
			if !selected {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			before := next
			cachedNext, cachedAdded, cached, cachedErr := scope.consumeInheritancePreparedAction(stage, definition, actionIndex, next, ref, changes)
			if cached {
				next, err = cachedNext, cachedErr
				if err == nil && cachedAdded.Column != nil {
					additions = append(additions, cachedAdded)
				}
			} else if table.RelationOptionsAction(action.Action) {
				next, err = table.RelationOptionsCandidate(next, action)
			} else if table.RelationshipAction(action.Action) {
				next, err = scope.inheritanceRelationshipCandidate(stage, next, actionIndex, action)
			} else if action.Action == parser.AddIdentity || action.Action == parser.DropIdentity || action.Action == parser.SetIdentity {
				stage = e.authorizedStage(stage, auth, op.Session)
				next, err = e.identityCandidate(scope, auth, stage, next, action)
			} else if action.Action == parser.SetExpression || action.Action == parser.DropExpression {
				var attribute int32
				next, attribute, err = e.generationCandidate(scope, op, auth, next, action)
				if err == nil && attribute != 0 && !slices.Contains(replacements, attribute) {
					replacements = append(replacements, attribute)
				}
			} else if action.Action == parser.DropColumn {
				if _, exists := next.Index[action.Column.Text]; exists {
					stage = e.authorizedStage(stage, auth, op.Session)
				}
				next, err = scope.dropColumnCandidate(stage, next, ref, action, changes)
			} else if action.Action == parser.AddColumn {
				var added table.AddedColumn
				next, added, err = e.addColumnCandidate(scope, op, auth, next, action, nil)
				if err == nil && added.Column != nil {
					additions = append(additions, added)
				}
			} else if action.Action == parser.AlterColumnType {
				var transformation index.ColumnTransformation
				captured := prepared[typeIndex]
				typeIndex++
				next, transformation, err = e.alterTypeCandidate(scope, op, auth, stage, old, next, ref, action, captured)
				if err == nil {
					transformations = append(transformations, transformation)
				}
			} else if action.Action == parser.AddUsingIndex {
				next, err = e.stageUsingIndex(op, stage, next, attachments[attachmentIndex])
				attachmentIndex++
			} else if action.Action == parser.AddUnique {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.AuthSession, stage.AuthRevision, stage.RoleRevision = op.Session, auth.Revision, auth.RoleRecord.Revision
				}
				next, err = e.stageAddedUnique(op, auth, stage, next, action.Unique)
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
				}
			} else if action.Action == parser.AddExclusion {
				stage = e.authorizedStage(stage, auth, op.Session)
				copy := *next
				copy.Constraints.Exclusion = slices.Clone(next.Constraints.Exclusion)
				next = &copy
				e.Mu.Lock()
				err = index.StageExclusion(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, scope.Input, next, action.Exclusion, true)
				e.Mu.Unlock()
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Exclusion[len(next.Constraints.Exclusion)-1].Index)
				}
			} else {
				next, err = table.NotNullCandidate(op, auth.Catalog, next, action, scope.inheritancePlan().NotNullMode(action.Column.Text))
			}
			if err == nil && action.Action == parser.AddNotNull {
				err = scope.inheritancePlan().OnlyCreatedConstraint(before, next)
			}
			if err != nil {
				return Result{}, err
			}
			scope.inheritancePlan().RecordAction(before, next, action)
		}
		if pass == 0 {
			if definition != nil {
				if definition.AfterDropOld != next {
					return Result{}, ids.ErrCatalogReplaced
				}
				if definition.AfterDropError != nil {
					err = definition.AfterDropError
				} else if definition.AfterDrop != nil {
					next, err = scope.consumePreparedDrop(stage, preparedDropOf(definition.AfterDrop), next, ref, changes)
				}
			} else {
				next, stage, err = e.stageDroppedConstraintIndexes(scope, auth, stage, old, next, s.Actions, changes)
			}
			if err == nil {
				cleanupView := auth.Catalog
				if stage != nil {
					cleanupView = e.StageRead(stage)
				}
				next = table.ReleaseRetiredTypeConstraints(cleanupView, next, s.Actions)
				next = table.ReleaseInheritedTypeConstraints(cleanupView, scope.inheritancePlan(), next, s.Actions)
			}
			if err == nil {
				pendingChecks, err = table.CaptureAlterTypeChecks(op, auth.Catalog, old, next, s.Actions)
			}
			if err == nil {
				pendingIndexes, err = captureAlterTypeIndexes(scope, auth.Catalog, old, next, s.Actions)
			}
		} else if pass == 1 {
			next, typeChecks, err = e.rebindAlterTypeChecks(scope, op, auth, ref, next, pendingChecks)
			if err == nil && len(pendingIndexes) > 0 {
				stage = e.authorizedStage(stage, auth, op.Session)
				err = e.rebindAlterTypeIndexes(scope, auth, next, stage, pendingIndexes)
			}
		}
		if err == nil && pass == 1 && hasAttachments {
			if stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
			}
			err = stage.Replace(next)
			if definition != nil && !definition.PhaseZeroOnly {
				if definition.UsingCaptureOld != next {
					return Result{}, ids.ErrCatalogReplaced
				}
				if definition.UsingCaptureError != nil {
					err = definition.UsingCaptureError
				} else {
					attachments = definition.UsingIndexes
				}
			} else {
				if err == nil {
					e.Mu.Lock()
					err = e.EvolveStageIndexesLocked(stage, ref.Address, transformations)
					if err == nil {
						attachments, err = index.CaptureUsingIndexes(DDLHost{E: e, Op: op, CatalogStage: stage}, next, s.Actions, transformations)
					}
					e.Mu.Unlock()
				}
			}
		}
		if err == nil && pass == 4 {
			next, err = table.AlterNotNullPass(op, auth.Catalog, StageDefinitions(stage), scope.inheritancePlan(), definition, next, additions, attachments)
		}
		if err == nil && pass == 6 {
			raw := table.AddedCheckSyntax(s.Actions, additions)
			if len(raw) != 0 && stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthSession, stage.AuthRevision, stage.RoleRevision = op.Session, auth.Revision, auth.RoleRecord.Revision
			}
			next, err = e.stageAddedChecks(scope, op, auth, stage, next, raw, &addedChecks)
		}
		if err != nil {
			return Result{}, index.DefaultClassDiagnostic(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, err)
		}
	}
	op.Definition = originalDefinition
	auth.Definition = nil
	finish, err := table.FinishAlterCandidate(table.AlterFinishInput{
		Old: old, Next: next, Actions: s.Actions, Prepared: definition != nil,
		Transformations: transformations, Replacements: replacements,
		FinishInheritance: scope.finishInheritanceAlterCandidate,
		RelationshipOnly:  relationshipOnly, ForeignKeys: hasForeignKeys,
		StageEmpty:  stage == nil || stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && len(stage.Sequence.StagedGenerators()) == 0,
		Destination: destination,
	})
	if err != nil {
		return Result{}, err
	}
	next, storedReplacements, changedValues := finish.Next, finish.StoredReplacements, finish.ChangedValues
	if relationshipOnly {
		stage.UpdateDescriptor(next)
		return Result{}, nil
	}
	if finish.NoOp {
		return Result{}, nil
	}
	// USING/CHECK demand must allocate from the AS replacement, not the live
	// generator. This statement overlay is discarded on failure, never installed
	// in the owner before Finish. Keep the exact generator pointers through merge.
	e.Mu.Lock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	if stage != nil {
		if err := e.mergeStageJournalLocked(stage, changes, grantCatalog, true); err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
	}
	scope.Catalog = changes
	e.Mu.Unlock()
	if next != old {
		next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
		next.Constraints.Check = slices.Clone(next.Constraints.Check)
		next.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
		e.Mu.Lock()
		err = table.AssignDefinitionOIDs(DDLHost{E: e}, next)
		e.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		if len(transformations) != 0 {
			if err := table.RetireRebuiltCheckHistory(old, next); err != nil {
				return Result{}, err
			}
		}
	}
	stage = e.authorizedStage(stage, auth, op.Session)
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	// Complete TYPE provider rebinding before adding queued FK definitions.
	// Their selected supporting keys now have the final private key shape.
	e.Mu.Lock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
	next, err = index.RebuildAlterUniques(h, next, transformations)
	if err == nil {
		next, err = table.RebuildAlterNotNulls(DDLHost{E: e, CatalogStage: stage}, next, transformations)
	}
	if err == nil {
		next, err = index.RebuildAlterExclusions(h, next, transformations)
	}
	if err == nil {
		err = e.EvolveStageIndexesLocked(stage, ref.Address, transformations)
	}
	if err == nil {
		next, err = table.RebuildAlterForeignKeys(DDLHost{E: e, CatalogStage: stage}, next, transformations)
	}
	if err == nil {
		next, err = table.RebindPhysicalNotNullType(stage, next, transformations)
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, index.DefaultClassDiagnostic(h, auth, err)
	}
	fkIndex := 0
	for _, action := range s.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		if fkIndex >= len(scope.foreignKeysOrZero().AlterKeys) {
			return Result{}, ids.ErrCatalogReplaced
		}
		capture := scope.foreignKeysOrZero().AlterKeys[fkIndex]
		next, err = e.stageAddedForeignKey(scope, auth, stage, next, action.ForeignKey, capture.Target.Address, capture.Index)
		if err != nil {
			return Result{}, err
		}
		addedForeignKeys = append(addedForeignKeys, next.Constraints.ForeignKeys[len(next.Constraints.ForeignKeys)-1].OID)
		fkIndex++
	}
	if hasForeignKeys && next.Revision() == old.Revision() {
		if err := table.BumpAlterRevision(old, next); err != nil {
			return Result{}, err
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if changedValues {
		if err := scope.acquireForeignKeyTypeProviders(auth.Catalog, old.Address()); err != nil {
			return Result{}, err
		}
	}
	if len(replacements) != 0 {
		if err := e.captureCheckSnapshots(scope, next); err != nil {
			return Result{}, err
		}
	}
	for _, added := range additions {
		if next.ColumnMeta.Defaults[added.Attribute].CatalogSnapshot {
			e.Mu.Lock()
			err = e.CaptureCommentSnapshotLocked(scope)
			e.Mu.Unlock()
			if err != nil {
				return Result{}, err
			}
			break
		}
	}
	fills, perRowAdd, err := table.PlanAddedColumns(alterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), additions)
	if err != nil {
		return Result{}, err
	}
	rewrite, rowRewrite := table.AlterRewrites(transformations, storedReplacements, changedValues, perRowAdd, fills)
	if rowRewrite {
		if err := typedef.RowRewriteError(auth.Catalog, old); err != nil {
			return Result{}, err
		}
	}
	if rewrite {
		scope.Owner.Mu.Lock()
		err = scope.AcquireSerializableSnapshot()
		if err == nil {
			e.Mu.Lock()
			err = e.SerializableReadLocked(scope, ref)
			e.Mu.Unlock()
		}
		scope.Owner.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	storageOwner := old
	if accumulated, exists := changes.Storage[ref.Address]; physicalRows && exists {
		shape = accumulated
		store = accumulated.Replacement
		storageOwner = accumulated.Relation
		versions, ends = store.Versions, store.Ends
	}
	shape.MergeFills(fills, next)
	replacement := store
	projectedWrites, projectedDeletes := scope.ColumnProjectionRoots(ref.Address)
	validator := table.NewAlterRowValidator(next, schema)
	if len(storedReplacements) != 0 {
		typeChecks = table.ValidCheckTraversal(next).Constraints.Check
	}
	if len(typeChecks) != 0 {
		traversal := *next
		traversal.Constraints.Check = typeChecks
		validator.Checks = &traversal
		if err := e.captureCheckSnapshots(scope, &traversal); err != nil {
			return Result{}, err
		}
	}
	if rewrite {
		// A rewrite creates the destination heap before evaluating old rows.
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return Result{}, err
		}
		writes, deletes := scope.ColumnProjectionRoots(storageOwner.Address())
		replacement, projectedWrites, projectedDeletes, err = table.RewriteTypeRows(alterRowEvaluation(scope, op, scope.RoutineAuth.Catalog), storageOwner, next, store, frontier, writes, deletes, transformations, additions, storedReplacements, validator)
		if err != nil {
			return Result{}, err
		}
		versions, ends = replacement.Versions, replacement.Ends
		shape.Rewritten, shape.Frontier = true, frontier
		shape.Snapshot, shape.HasSnapshot = nil, false
	} else if !slices.Equal(storageOwner.Attributes, next.Attributes) {
		shape, projectedWrites, projectedDeletes, err = projectColumnShape(scope, storageOwner, next, store, shape, snapshot, snapshotExists)
		if err != nil {
			return Result{}, err
		}
		replacement = shape.Replacement
		versions, ends = replacement.Versions, replacement.Ends
	}
	for _, oid := range index.DroppedUniqueOIDs(old, s.Actions) {
		if err := e.stagePartitionConstraintDrop(stage, oid); err != nil {
			return Result{}, err
		}
	}
	// Recapture effective authority after provider waits and row rewriting.
	// The immutable snapshot is used outside metadata locks for disclosure.
	e.Mu.Lock()
	err = scope.ValidateLockedRelation(e)
	var disclosure AuthorizationView
	if err == nil {
		disclosure, err = e.AuthorizationLocked(op)
		disclosure.Catalog = e.PrivateCatalogLocked(scope.Owner.Catalog)
		disclosure.Recapture(disclosure.Catalog)
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	indexEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: e.StageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	indexBuilds, err := index.ValidateAlterIndexBuilds(&indexEvaluation, disclosure.View, DisclosureEnv(&disclosure), e.StageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.Output, transformations, storedReplacements, addedIndexes, rewrite)
	if err != nil {
		return Result{}, index.DefaultClassDiagnostic(DDLHost{E: e, Op: op, CatalogStage: stage}, disclosure, err)
	}
	var indexOutputRows bool
	projectedWrites, indexOutputRows, err = index.AttachIndexBuildOutputs(op.Context(), projectedWrites, indexBuilds)
	if err != nil {
		return Result{}, err
	}
	if scanValidator, scan := table.PlanAlterScan(old, next, schema, rewrite, physicalRows, typeChecks, addedChecks, replacements, validator.NotNull); scan {
		if scanValidator.Checks != nil {
			if err := e.captureCheckSnapshots(scope, scanValidator.Checks); err != nil {
				return Result{}, err
			}
		}
		if err := table.ValidateAddedConstraintRows(alterRowEvaluation(scope, scope.Op, catalog.Read{}), scanValidator, versions, ends, frontier, projectedWrites, projectedDeletes); err != nil {
			return Result{}, err
		}
	}
	// Without a rewrite, old-row CHECK/NOT NULL validation precedes movement.
	if err := table.ValidateAlterTablespaceMove(destination); err != nil {
		return Result{}, err
	}
	dependencyTargets := table.DependencyTargets(ref.Address, old, next, transformations, replacements)
	for a := range stage.Removals() {
		if _, identity := stage.Sequence.IdentityOriginal(a); identity {
			continue
		}
		dependencyTargets = append(dependencyTargets, a)
	}
	if len(dependencyTargets) != 0 {
		changes.Dependencies = append(changes.Dependencies, session.DependencyObservation{grantCatalog, dependencyTargets})
	}
	changes.Dependencies = append(changes.Dependencies, stage.Sequence.IdentityDependencyList()...)
	if scope.Inheritance.Alter == nil {
		if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
			return Result{}, err
		}
	}
	// Validation closes the type graph before transferring it to private catalog
	// changes. Include dependent holder stores as well as the directly altered row.
	for address, rebound := range stage.RowShapes {
		consumer := stage.Get(address).(*catalog.Relation)
		changes.Storage[address] = session.StorageChange{Original: rebound.Original, Root: rebound.Original.Versions, Ends: rebound.ObservedEnds, Generation: rebound.Generation, Replacement: rebound.Replacement, Relation: consumer}
	}
	// Keep the pre-callback originals; Reset cannot restamp captured authority.
	e.Mu.Lock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	if hasAttachments || len(addedIndexes) != 0 {
		for name := range stage.Touched() {
			if e.Catalog.State.NameAddress(name) != grantCatalog.NameAddress(name) {
				e.Mu.Unlock()
				return Result{}, catalog.Conflict{}
			}
		}
	}
	if err := e.mergeStageJournalLocked(stage, changes, grantCatalog, false); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	shape, record, metadataOnly := table.AlterStorageShape(shape, replacement, old, next, changedValues, len(additions), physicalRows, hasAttachments)
	if record {
		changes.Storage[ref.Address] = shape
	}
	e.Mu.Unlock()
	if physicalRows && !rewrite {
		sourceWrites, _ := scope.ColumnProjectionRoots(ref.Address)
		var transported bool
		projectedWrites, transported, err = TransportIndexLayouts(&indexEvaluation, stage, changes, next, store, replacement, sourceWrites, projectedWrites)
		if err != nil {
			return Result{}, err
		}
		indexOutputRows = indexOutputRows || transported
	}
	if err := index.PrepareAlterIndexRoots(&indexEvaluation, changes, stage.Base, next, replacement, indexBuilds, metadataOnly); err != nil {
		return Result{}, err
	}
	if err := scope.prepareExclusionShape(old, next, e.StageRead(stage), projectedWrites, projectedDeletes); err != nil {
		return Result{}, err
	}
	// A shape statement replaces prior own roots only on the successful merge.
	if replacement != store || indexOutputRows {
		scope.StageProjectedColumnRoots(ref.Address, projectedWrites, projectedDeletes)
	}
	if changedValues {
		candidate := e.StageRead(stage)
		if err := scope.validateForeignKeys(candidate, table.ReferencingForeignKeys(candidate, ref.Address)); err != nil {
			return Result{}, err
		}
	}
	if len(transformations) == 0 && len(addedForeignKeys) != 0 {
		if err := scope.validateForeignKeys(e.StageRead(stage), table.AddedForeignKeys(next, addedForeignKeys)); err != nil {
			return Result{}, err
		}
	}
	if generators := stage.Sequence.StagedGenerators(); len(generators) != 0 {
		e.Mu.Lock()
		for address, generator := range generators {
			if memory := PayloadOf(op.Session).Sequences.Memory(address); memory != nil {
				memory.DiscardStaleTailLocked(generator.Generation)
			}
		}
		e.Mu.Unlock()
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
			return Result{}, err
		}
	}
	scope.Catalog = changes
	if scope.Inheritance.Alter == nil {
		view.EmitCascade(op, stage.DropCascadeNames)
	}
	return Result{}, nil
}
