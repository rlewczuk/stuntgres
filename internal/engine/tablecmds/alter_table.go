package tablecmds

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
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
// The caller holds core.Engine.mu. Every return releases it before callbacks/scanning.
func AlterTableLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, s *parser.AlterTable, count int, physicalWork *[]AlterTablePhysical) (core.Result, error) {
	scope.RoutineAuth = auth
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.ResolveRelationReference(auth, s.Name, true)
	if s.IfExists && table.RenameMissing(err) {
		return SkipMissingRelationLocked(e, scope, op, s.Name.Parts[len(s.Name.Parts)-1].Text, false)
	}
	if err == nil && ref != scope.LockedRef {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && !(scope.Inheritance.Alter != nil && scope.Inheritance.Alter.Descendant && table.ValidationOnly(s.Actions)) {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.LockedDescriptor.(*catalog.Relation)
	if s.Only && scope.Inheritance.Alter == nil && !catalog.HierarchyRelation(old) {
		s = table.OnlySetNotNull(auth.Catalog, old, s)
	}
	if core.PartitionUniqueReplayAllowed(op, auth, s, old) {
		return executePartitionUniqueReplayLocked(e, scope, op, auth, s, old)
	}
	if catalog.HierarchyRelation(old) && partition.ExclusionAddActions(s) {
		return executePartitionExclusionAddLocked(e, scope, op, auth, s, old)
	}
	if catalog.HierarchyRelation(old) && index.PartitionConstraintDropActions(old, s.Actions) {
		return executePartitionConstraintDropLocked(e, scope, auth, old, s)
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
		preparation, err = prepareAlterTableActions(e, scope, op, auth, old, ref, s.Actions)
		if err != nil {
			if len(preparation.Types) == 0 {
				return fail(err)
			}
			// A completed admission followed by a coercion failure still runs
			// the real sequence-AS prephase. Its private stage is discarded,
			// while the caller's already-discarded cache tail stays discarded.
			e.Mu.Unlock()
			stage, stageErr := stageAlterIdentityTypes(e, scope, op, auth, old, s.Actions, preparation.Types)
			if scope.Inheritance.Alter == nil {
				e.DiscardCatalogStage(stage)
			}
			if stageErr != nil {
				return core.Result{}, stageErr
			}
			return core.Result{}, err
		}
	}
	work, destination, prepared := preparation.Actions, preparation.Destination, preparation.Types
	if len(work) == 0 {
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, e.Catalog.State.Get(ref.Address))
		e.Mu.Unlock()
		return core.Result{}, nil
	}
	if len(work) != len(s.Actions) {
		command := *s
		command.Actions = work
		s = &command
	}
	if table.ValidationOnly(s.Actions) {
		if scope.Inheritance.Alter != nil && scope.Inheritance.Alter.Descendant {
			command := *s
			command.Actions = slices.Clone(s.Actions)
			for i, action := range command.Actions {
				mapped, mapErr := table.ValidationChildAction(old, action)
				if mapErr != nil {
					return fail(mapErr)
				}
				command.Actions[i] = mapped
			}
			s = &command
		}
		allValid := true
		for _, action := range s.Actions {
			selected, selectErr := table.SelectAlterConstraintValidation(old, action.Constraint.Text)
			if selectErr != nil {
				return fail(selectErr)
			}
			allValid = allValid && !selected.Invalid()
		}
		if allValid {
			// Relay original descendant intent without scanning this valid node.
			if scope.Inheritance.Alter != nil && scope.Inheritance.Alter.Descendant {
				for _, action := range s.Actions {
					inheritancePlan(scope).RecordAction(old, old, action)
				}
			}
			e.Mu.Unlock()
			return core.Result{}, nil
		}
	}
	if scope.Inheritance.Alter == nil && !access.RowSecurityActions(s.Actions) && !table.TriggerModesOnly(s.Actions) && !table.RuleModesOnly(s.Actions) && !table.RelationOptionsOnly(s.Actions) && (catalog.InheritanceParticipant(auth.Catalog, old) || table.HasRelationshipActions(s.Actions) || catalog.HierarchyRelation(old) && table.PartitionConstraintActions(old, s.Actions)) {
		return alterInheritanceLocked(e, scope, op, auth, s, count, destination, prepared)
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
		return stageRowSecurityFlagsLocked(e, scope, op, auth, old, access.RowSecurityFlags(old, s.Actions))
	}
	if table.RelationOptionsOnly(s.Actions) {
		next, err := table.RelationOptionsOnlyCandidate(old, s.Actions, destination)
		if err != nil {
			return fail(err)
		}
		return stageRowSecurityFlagsLocked(e, scope, op, auth, old, next)
	}
	if table.TriggerModesOnly(s.Actions) {
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		return triggerModesLocked(e, scope, op, auth, old, s)
	}
	if table.RuleModesOnly(s.Actions) {
		if err := table.ValidateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		next := old
		for _, action := range s.Actions {
			if err := op.Context().Err(); err != nil {
				return fail(err)
			}
			next, err = table.RuleModeCandidate(next, action)
			if err != nil {
				return fail(err)
			}
		}
		return stageRowSecurityFlagsLocked(e, scope, op, auth, old, next)
	}
	validityOnly := table.ValidationOnly(s.Actions)
	changes := scope.PendingCatalog()
	if scope.Inheritance.Alter == nil {
		changes = changes.Clone()
	}
	for _, capture := range foreignKeysOrZero(scope).ValidationKeys {
		if e.PrivateStorageLocked(changes, capture.Target.Address) != capture.Storage {
			return fail(ids.ErrCatalogReplaced)
		}
		if validityOnly {
			changes.AddFence(session.NewStorageIdentityFence(capture.Target.Address, e.Storage.Relations[capture.Target.Address]))
		} else {
			changes.AddFence(session.NewStorageReadFence(capture.Target.Address, e.Storage.Relations[capture.Target.Address]))
		}
		for _, source := range []*catalog.Relation{capture.Parent, capture.Provider} {
			if source == nil || auth.Catalog.Get(source.Address()) != source {
				return fail(ids.ErrCatalogReplaced)
			}
			if _, recorded := changes.Originals[source.Address()]; !recorded {
				changes.Originals[source.Address()] = e.Catalog.State.Get(source.Address())
			}
		}
	}
	relationshipOnly := table.RelationshipOnly(s.Actions)
	// Virtual partition nodes share the candidate/provider/publication pipeline,
	// but have no heap. The inheritance plan visits physical descendants with
	// their own storage, validators and journal fences.
	physicalRows := old.Partition == nil
	var original, store *storage.Relation
	var shape session.StorageChange
	var frontier ids.CommitSeq
	var snapshot visibility.Root
	var snapshotExists bool
	grantCatalog := e.Catalog.State
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
		if validityOnly {
			changes.AddFence(session.NewStorageIdentityFence(ref.Address, original))
		} else {
			changes.AddFence(session.NewStorageReadFence(ref.Address, original))
		}
		frontier = e.Visibility.CommitSeq()
		snapshot, snapshotExists = scope.Owner.Roots[ref.Address]
	}
	var columnDropPath []ids.ObjectAddress
	for _, action := range s.Actions {
		if action.Action == parser.DropColumn {
			columnDropPath, err = core.ReferencePath(auth)
			if err != nil {
				return fail(err)
			}
			break
		}
	}
	var stage *core.CatalogStage
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
		op.Definition = core.StageDefinitions(scope.Inheritance.Alter.Stage)
		auth.Definition = op.Definition
	}
	defer func() { op.Definition = originalDefinition }()
	preparedStage, err := stageAlterIdentityTypes(e, scope, op, auth, old, s.Actions, prepared)
	stage = preparedStage
	if stage == nil && scope.Inheritance.Alter != nil {
		stage = scope.Inheritance.Alter.Stage
	}
	if err != nil {
		return core.Result{}, err
	}
	next := old
	var stagedModes *catalog.Relation
	if state := scope.Inheritance.Alter; state != nil && state.Descendant {
		stagedModes, _ = state.Stage.Get(old.Address()).(*catalog.Relation)
	}
	definition := inheritancePreparedNode(scope)
	var transformations []index.ColumnTransformation
	var pendingChecks []table.AlterTypeCheck
	var pendingIndexes []table.AlterTypeIndex
	var typeChecks []catalog.CheckConstraint
	var addedChecks []catalog.CheckConstraint
	var addedIndexes []ids.ObjectAddress
	var foreignKeyHistory []catalog.ForeignKeyConstraint
	var validatedChecks []catalog.CheckConstraint
	var validatedNotNull []catalog.NotNullConstraint
	var additions []table.AddedColumn
	var replacements []int32
	var attachments []index.UsingIndexAttachment
	hasAttachments := false
	hasForeignKeys := len(foreignKeysOrZero(scope).AlterKeys) != 0
	hasValidations := slices.ContainsFunc(s.Actions, func(a parser.AlterTableAction) bool { return a.Action == parser.ValidateConstraint })
	hasRuleModes := slices.ContainsFunc(s.Actions, func(a parser.AlterTableAction) bool { return table.RuleModeAction(a.Action) })
	hasTriggerModes := slices.ContainsFunc(s.Actions, func(a parser.AlterTableAction) bool { return table.TriggerModeAction(a.Action) })
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
				return core.Result{}, err
			}
			before := next
			cachedNext, cachedAdded, cached, cachedErr := consumeInheritancePreparedAction(scope, stage, definition, actionIndex, next, ref, changes)
			if cached {
				next, err = cachedNext, cachedErr
				if err == nil && cachedAdded.Column != nil {
					additions = append(additions, cachedAdded)
				}
			} else if table.RelationOptionsAction(action.Action) {
				next, err = table.RelationOptionsCandidate(next, action)
			} else if table.RelationshipAction(action.Action) {
				next, err = inheritanceRelationshipCandidate(scope, stage, next, actionIndex, action)
			} else if action.Action == parser.AddIdentity || action.Action == parser.DropIdentity || action.Action == parser.SetIdentity {
				stage = authorizedStage(e, stage, auth, op.Session)
				next, err = identityCandidate(e, scope, auth, stage, next, action)
			} else if action.Action == parser.SetExpression || action.Action == parser.DropExpression {
				var attribute int32
				next, attribute, err = GenerationCandidate(e, scope, op, auth, next, action)
				if err == nil && attribute != 0 && !slices.Contains(replacements, attribute) {
					replacements = append(replacements, attribute)
				}
			} else if action.Action == parser.DropColumn {
				if _, exists := next.Index[action.Column.Text]; exists {
					stage = authorizedStage(e, stage, auth, op.Session)
					stage.DependencyPath = columnDropPath
				}
				next, err = DropColumnCandidate(scope, stage, next, ref, action, changes)
			} else if action.Action == parser.AddColumn {
				var added table.AddedColumn
				next, added, err = AddColumnCandidate(e, scope, op, auth, next, action, nil)
				if err == nil && added.Column != nil {
					additions = append(additions, added)
				}
			} else if action.Action == parser.AlterColumnType {
				var transformation index.ColumnTransformation
				captured := prepared[typeIndex]
				typeIndex++
				next, transformation, err = AlterTypeCandidate(e, scope, op, auth, stage, old, next, ref, action, captured)
				if err == nil {
					transformations = append(transformations, transformation)
				}
			} else if action.Action == parser.AddUsingIndex {
				next, err = stageUsingIndex(e, op, stage, next, attachments[attachmentIndex])
				attachmentIndex++
			} else if action.Action == parser.AddUnique {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.AuthorizeLocked(auth, op.Session)
				}
				next, err = stageAddedUnique(e, op, auth, stage, next, action.Unique)
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
				}
			} else if action.Action == parser.AddExclusion {
				stage = authorizedStage(e, stage, auth, op.Session)
				copy := *next
				copy.Constraints.Exclusion = slices.Clone(next.Constraints.Exclusion)
				next = &copy
				e.Mu.Lock()
				err = index.StageExclusion(TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, scope.Input, next, action.Exclusion, true)
				e.Mu.Unlock()
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Exclusion[len(next.Constraints.Exclusion)-1].Index)
				}
			} else {
				next, err = table.NotNullCandidate(op, auth.Catalog, next, action, inheritancePlan(scope).NotNullMode(action.Column.Text))
			}
			if err == nil && action.Action == parser.AddNotNull {
				err = inheritancePlan(scope).OnlyCreatedConstraint(before, next)
			}
			if err != nil {
				return core.Result{}, err
			}
			scope.RecordAlterDeletion(auth, before, next, action)
			inheritancePlan(scope).RecordAction(before, next, action)
		}
		if pass == 0 {
			if definition != nil {
				if definition.AfterDropOld != next {
					return core.Result{}, ids.ErrCatalogReplaced
				}
				if definition.AfterDropError != nil {
					err = definition.AfterDropError
				} else if definition.AfterDrop != nil {
					next, err = consumePreparedDrop(scope, stage, preparedDropOf(definition.AfterDrop), next, ref, changes)
				}
			} else {
				next, stage, err = stageDroppedConstraintIndexes(e, scope, auth, stage, old, next, s.Actions, changes)
			}
			if err == nil {
				cleanupView := auth.Catalog
				if stage != nil {
					cleanupView = e.StageRead(stage)
				}
				next = table.ReleaseRetiredTypeConstraints(cleanupView, next, s.Actions)
				next = table.ReleaseInheritedTypeConstraints(cleanupView, inheritancePlan(scope), next, s.Actions)
			}
			if err == nil {
				pendingChecks, err = table.CaptureAlterTypeChecks(op, auth.Catalog, old, next, s.Actions)
			}
			if err == nil {
				pendingIndexes, err = CaptureAlterTypeIndexes(scope, auth.Catalog, old, next, s.Actions)
			}
		} else if pass == 1 {
			next, typeChecks, err = RebindAlterTypeChecks(e, scope, op, auth, ref, next, pendingChecks)
			if err == nil && len(pendingIndexes) > 0 {
				stage = authorizedStage(e, stage, auth, op.Session)
				err = RebindAlterTypeIndexes(e, scope, auth, next, stage, pendingIndexes)
			}
		}
		if err == nil && pass == 1 && hasAttachments {
			if stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
			}
			err = stage.Replace(next)
			if definition != nil && !definition.PhaseZeroOnly {
				if definition.UsingCaptureOld != next {
					return core.Result{}, ids.ErrCatalogReplaced
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
						attachments, err = index.CaptureUsingIndexes(TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, next, s.Actions, transformations)
					}
					e.Mu.Unlock()
				}
			}
		}
		if err == nil && pass == 4 {
			next, err = table.AlterNotNullPass(op, auth.Catalog, core.StageDefinitions(stage), inheritancePlan(scope), definition, next, additions, attachments)
		}
		if err == nil && pass == 6 {
			raw := table.AddedCheckSyntax(s.Actions, additions)
			if len(raw) != 0 && stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, op.Session)
			}
			next, err = stageAddedChecks(e, scope, op, auth, stage, next, raw, &addedChecks)
		}
		if err != nil {
			return core.Result{}, index.DefaultClassDiagnostic(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, err)
		}
	}
	next = table.PreserveTriggerModes(stagedModes, next)
	op.Definition = originalDefinition
	auth.Definition = nil
	finish, err := table.FinishAlterCandidate(table.AlterFinishInput{
		Old: old, Next: next, Actions: s.Actions, Prepared: definition != nil,
		Transformations: transformations, Replacements: replacements,
		FinishInheritance: func(o, n *catalog.Relation) (*catalog.Relation, error) {
			return finishInheritanceAlterCandidate(scope, o, n)
		},
		RelationshipOnly: relationshipOnly, ForeignKeys: hasForeignKeys, ValidateConstraints: hasValidations || hasRuleModes || hasTriggerModes,
		StageEmpty:  stage == nil || stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && len(stage.Sequence.StagedGenerators()) == 0,
		Destination: destination,
	})
	if err != nil {
		return core.Result{}, err
	}
	next, storedReplacements, changedValues := finish.Next, finish.StoredReplacements, finish.ChangedValues
	if relationshipOnly {
		stage.UpdateDescriptor(next)
		return core.Result{}, nil
	}
	if finish.NoOp {
		return core.Result{}, nil
	}
	// The actual identity-AS validation/cache prephase has already run. Keep
	// its exact staged metadata/generators for the ordinary validated transfer
	// before rewrite callbacks and volatile DEFAULT/USING execution.
	e.Mu.Lock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return core.Result{}, ids.ErrCatalogReplaced
	}
	if stage != nil {
		if err := validateAlterSequenceOriginalsLocked(e, stage, grantCatalog); err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
	}
	scope.Catalog = changes
	e.Mu.Unlock()
	if next != old {
		next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
		next.Constraints.Check = slices.Clone(next.Constraints.Check)
		next.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
		e.Mu.Lock()
		err = table.AssignDefinitionOIDs(core.DDLHost{E: e}, next)
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		if len(transformations) != 0 {
			if err := table.RetireRebuiltCheckHistory(old, next); err != nil {
				return core.Result{}, err
			}
		}
	}
	stage = authorizedStage(e, stage, auth, op.Session)
	if err := stage.Replace(next); err != nil {
		return core.Result{}, err
	}
	// Complete TYPE provider rebinding before adding queued FK definitions.
	// Their selected supporting keys now have the final private key shape.
	e.Mu.Lock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return core.Result{}, ids.ErrCatalogReplaced
	}
	h := TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}
	next, err = index.RebuildAlterUniques(h, next, transformations)
	if err == nil {
		next, err = table.RebuildAlterNotNulls(core.DDLHost{E: e, CatalogStage: stage}, next, transformations)
	}
	if err == nil {
		next, err = index.RebuildAlterExclusions(h, next, transformations)
	}
	if err == nil {
		err = e.EvolveStageIndexesLocked(stage, ref.Address, transformations)
	}
	if err == nil {
		next, err = table.RebuildAlterForeignKeys(core.DDLHost{E: e, CatalogStage: stage}, next, transformations)
	}
	if err == nil {
		next, err = table.RebindPhysicalNotNullType(stage, next, transformations)
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, index.DefaultClassDiagnostic(h, auth, err)
	}
	fkIndex := 0
	for _, action := range s.Actions {
		if action.Action != parser.AddForeignKey {
			continue
		}
		if fkIndex >= len(foreignKeysOrZero(scope).AlterKeys) {
			return core.Result{}, ids.ErrCatalogReplaced
		}
		capture := foreignKeysOrZero(scope).AlterKeys[fkIndex]
		next, err = stageAddedForeignKey(e, scope, auth, stage, next, action.ForeignKey, capture.Target.Address, capture.Index)
		if err != nil {
			return core.Result{}, err
		}
		added := next.Constraints.ForeignKeys[len(next.Constraints.ForeignKeys)-1]
		// Capture ADD-pass eligibility before MISC can promote NOT VALID.
		if !added.NotValid {
			foreignKeyHistory = append(foreignKeyHistory, added)
		}
		fkIndex++
	}
	// Misc resolves only after every ADD definition, including deferred FKs.
	for _, action := range s.Actions {
		if err := op.Context().Err(); err != nil {
			return core.Result{}, err
		}
		if table.TriggerModeAction(action.Action) {
			if err := stage.ReplaceDescriptor(next, false, false); err != nil {
				return core.Result{}, err
			}
			only := s.Only
			if state := inheritancePlan(scope); state != nil && !state.Descendant {
				only = state.Only
			}
			next, err = stageTriggerMode(e, scope, auth, stage, next, action, only)
			if err != nil {
				return core.Result{}, err
			}
			continue
		}
		if table.RuleModeAction(action.Action) {
			next, err = table.RuleModeCandidate(next, action)
			if err != nil {
				return core.Result{}, err
			}
			continue
		}
		if action.Action != parser.ValidateConstraint {
			continue
		}
		if plan := inheritancePlan(scope); plan != nil && plan.Descendant {
			mapped, mapErr := table.ValidationChildAction(next, action)
			if mapErr != nil {
				return core.Result{}, mapErr
			}
			action = mapped
		}
		before := next
		promoted, selected, selectErr := table.ValidateConstraintCandidate(next, action.Constraint.Text)
		if selectErr != nil {
			return core.Result{}, selectErr
		}
		plan := inheritancePlan(scope)
		if plan != nil && !plan.Descendant && plan.Only && len(plan.Targets) > 1 && selected.Recursive() {
			return core.Result{}, core.SQLError("42P16", "constraint must be validated on child tables too")
		}
		next = promoted
		plan.RecordAction(before, next, action)
		if before == next {
			continue
		}
		switch {
		case selected.Check != nil:
			validatedChecks = append(validatedChecks, *selected.Check)
		case selected.NotNull != nil:
			validatedNotNull = append(validatedNotNull, *selected.NotNull)
		case selected.ForeignKey != nil:
			foreignKeyHistory = append(foreignKeyHistory, *selected.ForeignKey)
		}
	}
	if (hasValidations || hasRuleModes || hasTriggerModes) && next != old {
		if err := stage.Replace(next); err != nil {
			return core.Result{}, err
		}
		if next.Revision() == old.Revision() {
			if err := table.BumpAlterRevision(old, next); err != nil {
				return core.Result{}, err
			}
		}
	}
	if hasForeignKeys && next.Revision() == old.Revision() {
		if err := table.BumpAlterRevision(old, next); err != nil {
			return core.Result{}, err
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	if changedValues {
		if err := acquireForeignKeyTypeProviders(scope, auth.Catalog, old.Address()); err != nil {
			return core.Result{}, err
		}
	}
	if len(replacements) != 0 {
		if err := CaptureCheckSnapshots(e, scope, next); err != nil {
			return core.Result{}, err
		}
	}
	for _, added := range additions {
		if next.ColumnMeta.Defaults[added.Attribute].CatalogSnapshot {
			e.Mu.Lock()
			err = e.CaptureCommentSnapshotLocked(scope)
			e.Mu.Unlock()
			if err != nil {
				return core.Result{}, err
			}
			break
		}
	}
	fills, perRowAdd, err := table.PlanAddedColumns(AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), additions)
	if err != nil {
		return core.Result{}, err
	}
	rewrite, rowRewrite := table.AlterRewrites(transformations, storedReplacements, changedValues, perRowAdd, fills)
	if rowRewrite {
		if err := typedef.RowRewriteError(auth.Catalog, old); err != nil {
			return core.Result{}, err
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
			return core.Result{}, err
		}
	}
	for _, oid := range index.DroppedUniqueOIDs(old, s.Actions) {
		if err := stagePartitionConstraintDrop(e, stage, oid); err != nil {
			return core.Result{}, err
		}
	}
	changes = scope.PendingCatalog()
	dependencyTargets := table.DependencyTargets(ref.Address, old, next, transformations, replacements)
	for a := range stage.Removals() {
		if _, identity := stage.Sequence.IdentityOriginal(a); !identity {
			dependencyTargets = append(dependencyTargets, a)
		}
	}
	if len(dependencyTargets) != 0 {
		changes.Dependencies = append(changes.Dependencies, session.DependencyObservation{grantCatalog, dependencyTargets})
	}
	storageOwner := old
	if accumulated, exists := changes.Storage[ref.Address]; physicalRows && exists {
		shape, store, storageOwner = accumulated, accumulated.Replacement, accumulated.Relation
	}
	physical := AlterTablePhysical{
		mutation: scope.StatementMutation, ref: ref, old: old, storageOwner: storageOwner, store: store, shape: shape,
		frontier: frontier, snapshot: snapshot, snapshotExists: snapshotExists,
		transformations: transformations, additions: additions, storedReplacements: storedReplacements, replacements: replacements,
		typeChecks: typeChecks, addedChecks: addedChecks, addedIndexes: addedIndexes,
		validatedChecks: validatedChecks, validatedNotNull: validatedNotNull, foreignKeyHistory: foreignKeyHistory,
		fills: fills, rewrite: rewrite, rewriteEvent: physicalRows && table.AlterEventRewrite(transformations, storedReplacements, perRowAdd),
		physicalRows: physicalRows, hasAttachments: hasAttachments, changedValues: changedValues, destination: destination,
		validityOnly: validityOnly,
	}
	if physicalWork != nil {
		*physicalWork = append(*physicalWork, physical)
		return core.Result{}, nil
	}
	layouts := stage.IndexesOrZero().Layouts
	phase, err := e.PublishAlterMetadata(scope, stage, map[ids.ObjectAddress]bool{ref.Address: true}, map[ids.ObjectAddress]bool{ref.Address: validityOnly})
	if err != nil {
		return core.Result{}, err
	}
	result, err := finishAlterPhysical(e, scope, phase, &physical, layouts)
	if err == nil {
		err = e.FinishAlterValidation(scope, phase)
	}
	if err == nil {
		view.EmitCascade(op, stage.DropCascadeNames)
	}
	return result, err
}
