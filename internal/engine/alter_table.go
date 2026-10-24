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
	scope.routineAuth = auth
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, s.Name, true)
	if s.IfExists && table.RenameMissing(err) {
		return e.skipMissingRelationLocked(scope, op, s.Name.Parts[len(s.Name.Parts)-1].Text, false)
	}
	if err == nil && ref != scope.lockedRef {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*catalog.Relation)
	if s.Only && scope.inheritance.alter == nil && !catalog.HierarchyRelation(old) {
		s = table.OnlySetNotNull(auth.Catalog, old, s)
	}
	if partitionUniqueReplayAllowed(op, auth, s, old) {
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
	if scope.inheritance.alter != nil && !scope.inheritance.alter.Descendant {
		preparation = table.AlterTablePreparation{Actions: s.Actions, Destination: scope.inheritance.alter.Tablespace, Types: scope.inheritance.alter.Types}
	} else {
		preparation, err = e.prepareAlterTableActions(scope, op, auth, old, ref, s.Actions)
		if err != nil {
			if len(preparation.Types) == 0 {
				return fail(err)
			}
			// A completed admission followed by a coercion failure still runs
			// the real sequence-AS prephase. Its private stage is discarded,
			// while the caller's already-discarded cache tail stays discarded.
			e.mu.Unlock()
			stage, stageErr := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, preparation.Types)
			if scope.inheritance.alter == nil {
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
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
		e.mu.Unlock()
		return Result{}, nil
	}
	if len(work) != len(s.Actions) {
		command := *s
		command.Actions = work
		s = &command
	}
	if scope.inheritance.alter == nil && !access.RowSecurityActions(s.Actions) && !table.RelationOptionsOnly(s.Actions) && (catalog.InheritanceParticipant(auth.Catalog, old) || table.HasRelationshipActions(s.Actions) || catalog.HierarchyRelation(old) && table.PartitionConstraintActions(old, s.Actions)) {
		return e.alterInheritanceLocked(scope, op, auth, s, count, destination, prepared)
	}
	if scope.inheritance.alter == nil || !scope.inheritance.alter.Descendant {
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
	changes := scope.owner.Catalog.Clone()
	if scope.inheritance.alter != nil {
		changes = scope.inheritance.alter.changes
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
	grantCatalog := e.catalog.state
	schema := auth.Catalog.Get(old.Namespace()).Name()
	if !relationshipOnly && physicalRows {
		original = e.storage.Relations[ref.Address]
		store = e.privateStorageLocked(changes, ref.Address)
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
		versions, ends, frontier = store.Versions, store.Ends, e.visibility.CommitSeq()
		snapshot, snapshotExists = scope.owner.Roots[ref.Address]
	}
	var stage *CatalogStage
	if scope.inheritance.alter == nil || !scope.inheritance.alter.Descendant {
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
	}
	e.mu.Unlock()
	defer func() {
		if scope.inheritance.alter == nil {
			e.DiscardCatalogStage(stage)
		}
	}()
	originalDefinition := op.Definition
	if scope.inheritance.alter != nil {
		op.Definition = stageDefinitions(scope.inheritance.alter.stage)
		auth.definition = op.Definition
	}
	defer func() { op.Definition = originalDefinition }()
	preparedStage, err := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, prepared)
	stage = preparedStage
	if stage == nil && scope.inheritance.alter != nil {
		stage = scope.inheritance.alter.stage
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
					stage.authSession, stage.authRevision, stage.roleRevision = op.Session, auth.Revision, auth.RoleRecord.Revision
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
				e.mu.Lock()
				err = index.StageExclusion(ddlHost{e: e, op: op, stage: stage}, auth, scope.input, next, action.Exclusion, true)
				e.mu.Unlock()
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
					cleanupView = e.stageRead(stage)
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
					e.mu.Lock()
					err = e.evolveStageIndexesLocked(stage, ref.Address, transformations)
					if err == nil {
						attachments, err = index.CaptureUsingIndexes(ddlHost{e: e, op: op, stage: stage}, next, s.Actions, transformations)
					}
					e.mu.Unlock()
				}
			}
		}
		if err == nil && pass == 4 {
			next, err = table.AlterNotNullPass(op, auth.Catalog, stageDefinitions(stage), scope.inheritancePlan(), definition, next, additions, attachments)
		}
		if err == nil && pass == 6 {
			raw := table.AddedCheckSyntax(s.Actions, additions)
			if len(raw) != 0 && stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.authSession, stage.authRevision, stage.roleRevision = op.Session, auth.Revision, auth.RoleRecord.Revision
			}
			next, err = e.stageAddedChecks(scope, op, auth, stage, next, raw, &addedChecks)
		}
		if err != nil {
			return Result{}, err
		}
	}
	op.Definition = originalDefinition
	auth.definition = nil
	finish, err := table.FinishAlterCandidate(table.AlterFinishInput{
		Old: old, Next: next, Actions: s.Actions, Prepared: definition != nil,
		Transformations: transformations, Replacements: replacements,
		FinishInheritance: scope.finishInheritanceAlterCandidate,
		RelationshipOnly:  relationshipOnly, ForeignKeys: hasForeignKeys,
		StageEmpty:  stage == nil || stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && len(stage.sequence.StagedGenerators()) == 0,
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
	e.mu.Lock()
	if ref.Epoch != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	if stage != nil {
		if err := e.mergeStageJournalLocked(stage, changes, grantCatalog, true); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	scope.catalog = changes
	e.mu.Unlock()
	if next != old {
		next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
		next.Constraints.Check = slices.Clone(next.Constraints.Check)
		next.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
		e.mu.Lock()
		err = table.AssignDefinitionOIDs(ddlHost{e: e}, next)
		e.mu.Unlock()
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
	e.mu.Lock()
	if ref.Epoch != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	h := ddlHost{e: e, op: op, stage: stage}
	next, err = index.RebuildAlterUniques(h, next, transformations)
	if err == nil {
		next, err = table.RebuildAlterNotNulls(ddlHost{e: e, stage: stage}, next, transformations)
	}
	if err == nil {
		next, err = index.RebuildAlterExclusions(h, next, transformations)
	}
	if err == nil {
		err = e.evolveStageIndexesLocked(stage, ref.Address, transformations)
	}
	if err == nil {
		next, err = table.RebuildAlterForeignKeys(ddlHost{e: e, stage: stage}, next, transformations)
	}
	if err == nil {
		next, err = table.RebindPhysicalNotNullType(stage, next, transformations)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
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
	if err := scope.acquireDefinitionProviders(stage); err != nil {
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
			e.mu.Lock()
			err = e.captureCommentSnapshotLocked(scope)
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
			break
		}
	}
	fills, perRowAdd, err := table.PlanAddedColumns(alterRowEvaluation(scope, scope.op, scope.routineAuth.Catalog), additions)
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
		scope.owner.Mu.Lock()
		err = scope.acquireSerializableSnapshot()
		if err == nil {
			e.mu.Lock()
			err = e.serializableReadLocked(scope, ref)
			e.mu.Unlock()
		}
		scope.owner.Mu.Unlock()
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
	projectedWrites, projectedDeletes := scope.columnProjectionRoots(ref.Address)
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
		writes, deletes := scope.columnProjectionRoots(storageOwner.Address())
		replacement, projectedWrites, projectedDeletes, err = table.RewriteTypeRows(alterRowEvaluation(scope, op, scope.routineAuth.Catalog), storageOwner, next, store, frontier, writes, deletes, transformations, additions, storedReplacements, validator)
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
	e.mu.Lock()
	err = scope.validateLockedRelation(e)
	var disclosure AuthorizationView
	if err == nil {
		disclosure, err = e.authorizationLocked(op)
		disclosure.Catalog = e.privateCatalogLocked(scope.owner.Catalog)
		disclosure.Recapture(disclosure.Catalog)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	indexEvaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: e.stageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	indexBuilds, err := index.ValidateAlterIndexBuilds(&indexEvaluation, disclosure.View, disclosureEnv(&disclosure), e.stageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.output, transformations, storedReplacements, addedIndexes, rewrite)
	if err != nil {
		return Result{}, err
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
		if err := table.ValidateAddedConstraintRows(alterRowEvaluation(scope, scope.op, catalog.Read{}), scanValidator, versions, ends, frontier, projectedWrites, projectedDeletes); err != nil {
			return Result{}, err
		}
	}
	// Without a rewrite, old-row CHECK/NOT NULL validation precedes movement.
	if err := table.ValidateAlterTablespaceMove(destination); err != nil {
		return Result{}, err
	}
	dependencyTargets := table.DependencyTargets(ref.Address, old, next, transformations, replacements)
	for a := range stage.Removals() {
		if _, identity := stage.sequence.IdentityOriginal(a); identity {
			continue
		}
		dependencyTargets = append(dependencyTargets, a)
	}
	if len(dependencyTargets) != 0 {
		changes.Dependencies = append(changes.Dependencies, session.DependencyObservation{grantCatalog, dependencyTargets})
	}
	changes.Dependencies = append(changes.Dependencies, stage.sequence.IdentityDependencyList()...)
	if scope.inheritance.alter == nil {
		if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
			return Result{}, err
		}
	}
	// Validation closes the type graph before transferring it to private catalog
	// changes. Include dependent holder stores as well as the directly altered row.
	for address, rebound := range stage.rowShapes {
		consumer := stage.Get(address).(*catalog.Relation)
		changes.Storage[address] = session.StorageChange{Original: rebound.Original, Root: rebound.Original.Versions, Ends: rebound.ObservedEnds, Generation: rebound.Generation, Replacement: rebound.Replacement, Relation: consumer}
	}
	// Keep the pre-callback originals; Reset cannot restamp captured authority.
	e.mu.Lock()
	if ref.Epoch != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	if hasAttachments || len(addedIndexes) != 0 {
		for name := range stage.Touched() {
			if e.catalog.state.NameAddress(name) != grantCatalog.NameAddress(name) {
				e.mu.Unlock()
				return Result{}, catalog.Conflict{}
			}
		}
	}
	if err := e.mergeStageJournalLocked(stage, changes, grantCatalog, false); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	shape, record, metadataOnly := table.AlterStorageShape(shape, replacement, old, next, changedValues, len(additions), physicalRows, hasAttachments)
	if record {
		changes.Storage[ref.Address] = shape
	}
	e.mu.Unlock()
	if physicalRows && !rewrite {
		sourceWrites, _ := scope.columnProjectionRoots(ref.Address)
		var transported bool
		projectedWrites, transported, err = transportIndexLayouts(&indexEvaluation, stage, changes, next, store, replacement, sourceWrites, projectedWrites)
		if err != nil {
			return Result{}, err
		}
		indexOutputRows = indexOutputRows || transported
	}
	if err := index.PrepareAlterIndexRoots(&indexEvaluation, changes, stage.Base, next, replacement, indexBuilds, metadataOnly); err != nil {
		return Result{}, err
	}
	if err := scope.prepareExclusionShape(old, next, e.stageRead(stage), projectedWrites, projectedDeletes); err != nil {
		return Result{}, err
	}
	// A shape statement replaces prior own roots only on the successful merge.
	if replacement != store || indexOutputRows {
		scope.stageProjectedColumnRoots(ref.Address, projectedWrites, projectedDeletes)
	}
	if changedValues {
		candidate := e.stageRead(stage)
		if err := scope.validateForeignKeys(candidate, table.ReferencingForeignKeys(candidate, ref.Address)); err != nil {
			return Result{}, err
		}
	}
	if len(transformations) == 0 && len(addedForeignKeys) != 0 {
		if err := scope.validateForeignKeys(e.stageRead(stage), table.AddedForeignKeys(next, addedForeignKeys)); err != nil {
			return Result{}, err
		}
	}
	if generators := stage.sequence.StagedGenerators(); len(generators) != 0 {
		e.mu.Lock()
		for address, generator := range generators {
			if memory := payloadOf(op.Session).sequences.Memory(address); memory != nil {
				memory.DiscardStaleTailLocked(generator.Generation)
			}
		}
		e.mu.Unlock()
	}
	if rewrite {
		scope.owner.Mu.Lock()
		e.mu.Lock()
		err = scope.validateLockedRelation(e)
		if err == nil {
			event := [1]visibility.SerializableWrite{{Rewrite: true}}
			err = e.serializableWriteLocked(scope, ref, event[:])
		}
		e.mu.Unlock()
		scope.owner.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	scope.catalog = changes
	if scope.inheritance.alter == nil {
		view.EmitCascade(op, stage.dropCascadeNames)
	}
	return Result{}, nil
}
