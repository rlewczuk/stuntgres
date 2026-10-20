package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Named ALTER lookup rejects protected relations before action preparation.
func alterTableLookupKind(d *catalog.Relation) error {
	switch d.RelationKind {
	case catalog.CatalogRelation:
		return sqlError("42501", fmt.Sprintf(`permission denied: "%s" is a system catalog`, d.Name()))
	case catalog.CompositeRelation:
		diagnostic := sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, d.Name())).(*sqlstate.Error)
		diagnostic.Hint = "Use ALTER TYPE instead."
		return diagnostic
	default:
		return nil
	}
}

// The strictest captured ALTER grant protects the incarnation until Finish.
// The caller holds Engine.mu. Every return releases it before callbacks/scanning.
func (e *Engine) alterTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTable, count int) (Result, error) {
	scope.routineAuth = auth
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, s.Name, true)
	if s.IfExists && renameMissing(err) {
		op.session.mu.Lock()
		err = publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.completed = true
		e.mu.Unlock()
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, s.Name.Parts[len(s.Name.Parts)-1].Text)})
		return Result{}, nil
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
	if s.Only && scope.inheritanceAlter == nil && slices.ContainsFunc(s.Actions, func(a parser.AlterTableAction) bool { return a.Action == parser.SetNotNull }) {
		command := *s
		command.Actions = slices.Clone(s.Actions)
		for i := range command.Actions {
			if command.Actions[i].Action == parser.SetNotNull {
				a := &command.Actions[i]
				a.NoInherit = len(inheritanceChildren(auth.Catalog, old.Address())) != 0
				if slot, ok := old.Index[a.Column.Text]; ok {
					for _, c := range old.Constraints.NotNull {
						if c.Attribute == old.Attributes[slot] && c.NoInherit {
							a.NoInherit = true
						}
					}
				}
			}
		}
		s = &command
	}
	if partitionUniqueReplayAllowed(op, auth, s, old) {
		return e.executePartitionUniqueReplayLocked(scope, op, auth, s, old)
	}
	if hierarchyRelation(old) && partitionExclusionAddActions(s) {
		return e.executePartitionExclusionAddLocked(scope, op, auth, s, old)
	}
	if hierarchyRelation(old) && partitionConstraintDropActions(old, s.Actions) {
		return e.executePartitionConstraintDropLocked(scope, auth, old, s)
	}
	if len(s.Actions) == 0 {
		return fail(fmt.Errorf("stuntgres: empty ALTER TABLE"))
	}
	if err := typedef.TypedDirectActionsGuard(old, s.Actions); err != nil {
		return fail(err)
	}
	var preparation alterTablePreparation
	if scope.inheritanceAlter != nil && !scope.inheritanceAlter.descendant {
		preparation = alterTablePreparation{actions: s.Actions, destination: scope.inheritanceAlter.tablespace, types: scope.inheritanceAlter.types}
	} else {
		preparation, err = e.prepareAlterTableActions(scope, op, auth, old, ref, s.Actions)
		if err != nil {
			if len(preparation.types) == 0 {
				return fail(err)
			}
			// A completed admission followed by a coercion failure still runs
			// the real sequence-AS prephase. Its private stage is discarded,
			// while the caller's already-discarded cache tail stays discarded.
			e.mu.Unlock()
			stage, stageErr := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, preparation.types)
			if scope.inheritanceAlter == nil {
				e.DiscardCatalogStage(stage)
			}
			if stageErr != nil {
				return Result{}, stageErr
			}
			return Result{}, err
		}
	}
	work, destination, prepared := preparation.actions, preparation.destination, preparation.types
	if len(work) == 0 {
		if err := validateAlterTablespaceMove(destination); err != nil {
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
	if scope.inheritanceAlter == nil && !rowSecurityActions(s.Actions) && inheritanceParticipant(auth.Catalog, old) {
		return e.alterInheritanceLocked(scope, op, auth, s, count, destination, prepared)
	}
	if scope.inheritanceAlter == nil || !scope.inheritanceAlter.descendant {
		if err := inheritanceAlterActionsGuard(auth.Catalog, old, s.Actions); err != nil {
			return fail(err)
		}
	}
	if err := verifyAlterPrimaryPrerequisites(old, s.Actions); err != nil {
		return fail(err)
	}
	if rowSecurityActions(s.Actions) {
		if err := validateAlterTablespaceMove(destination); err != nil {
			return fail(err)
		}
		return e.stageRowSecurityFlagsLocked(scope, op, auth, old, rowSecurityFlags(old, s.Actions))
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	if scope.inheritanceAlter != nil {
		changes = scope.inheritanceAlter.changes
	}
	original := e.storage.Relations[ref.Address]
	store := e.privateStorageLocked(changes, ref.Address)
	if store == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	var shape catalogStorageChange
	exists := false
	if changes != nil {
		shape, exists = changes.storage[ref.Address]
	}
	if !exists {
		shape = catalogStorageChange{original: original, root: original.Versions, ends: original.Ends, generation: original.Generation, replacement: store, relation: old}
	}
	versions, ends, frontier := store.Versions, store.Ends, e.visibility.CommitSeq()
	snapshot, snapshotExists := scope.owner.roots[ref.Address]
	grantCatalog := e.catalog.state
	schema := auth.Catalog.Get(old.Namespace()).Name()
	var stage *CatalogStage
	if scope.inheritanceAlter == nil || !scope.inheritanceAlter.descendant {
		scope.catalogAuthority = auth.catalogAuthorityLocked(ref, e.catalog.state.Get(ref.Address))
	}
	e.mu.Unlock()
	defer func() {
		if scope.inheritanceAlter == nil {
			e.DiscardCatalogStage(stage)
		}
	}()
	originalDefinition := op.definition
	if scope.inheritanceAlter != nil {
		op.definition = stageDefinitionReader{scope.inheritanceAlter.stage}
		auth.definition = op.definition
	}
	defer func() { op.definition = originalDefinition }()
	preparedStage, err := e.stageAlterIdentityTypes(scope, op, auth, old, s.Actions, prepared)
	stage = preparedStage
	if stage == nil && scope.inheritanceAlter != nil {
		stage = scope.inheritanceAlter.stage
	}
	if err != nil {
		return Result{}, err
	}
	next := old
	definition := scope.inheritancePreparedNode()
	var transformations []columnTransformation
	var pendingChecks []alterTypeCheck
	var pendingIndexes []alterTypeIndex
	var typeChecks []catalog.CheckConstraint
	var addedChecks []catalog.CheckConstraint
	var addedIndexes []ids.ObjectAddress
	var addedForeignKeys []ids.OID
	var additions []addedColumn
	var replacements []int32
	var attachments []usingIndexAttachment
	hasAttachments := false
	hasForeignKeys := len(scope.foreignActionsOrZero().alterKeys) != 0
	for _, action := range s.Actions {
		hasAttachments = hasAttachments || action.Action == parser.AddUsingIndex
	}
	// Keep shape/generated/identity phases. Index metadata precedes other
	// constraint bindings; historical row checks run only after index builds.
	for pass := range 9 {
		typeIndex := 0
		attachmentIndex := 0
		for actionIndex, action := range s.Actions {
			selected := alterActionPhase(action.Action) == pass
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
				if err == nil && cachedAdded.column != nil {
					additions = append(additions, cachedAdded)
				}
			} else if action.Action == parser.AddIdentity || action.Action == parser.DropIdentity || action.Action == parser.SetIdentity {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.session)
				}
				next, err = e.identityCandidate(scope, auth, stage, next, action)
			} else if action.Action == parser.SetExpression || action.Action == parser.DropExpression {
				var attribute int32
				next, attribute, err = e.generationCandidate(scope, op, auth, next, action)
				if err == nil && attribute != 0 && !slices.Contains(replacements, attribute) {
					replacements = append(replacements, attribute)
				}
			} else if action.Action == parser.DropColumn {
				if _, exists := next.Index[action.Column.Text]; exists && stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.session)
				}
				next, err = scope.dropColumnCandidate(stage, next, ref, action, changes)
			} else if action.Action == parser.AddColumn {
				var added addedColumn
				next, added, err = e.addColumnCandidate(scope, op, auth, next, action, nil)
				if err == nil && added.column != nil {
					additions = append(additions, added)
				}
			} else if action.Action == parser.AlterColumnType {
				var transformation columnTransformation
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
					stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.Revision, auth.RoleRecord.Revision
				}
				next, err = e.stageAddedUnique(op, auth, stage, next, action.Unique)
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Unique[len(next.Constraints.Unique)-1].Index)
				}
			} else if action.Action == parser.AddExclusion {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.session)
				}
				copy := *next
				copy.Constraints.Exclusion = slices.Clone(next.Constraints.Exclusion)
				next = &copy
				e.mu.Lock()
				err = e.stageExclusion(op, scope.input, auth, stage, next, action.Exclusion, true)
				e.mu.Unlock()
				if err == nil {
					addedIndexes = append(addedIndexes, next.Constraints.Exclusion[len(next.Constraints.Exclusion)-1].Index)
				}
			} else {
				next, err = notNullCandidate(op, auth.Catalog, next, action, scope.inheritanceNotNullMode(action.Column.Text))
			}
			if err == nil && action.Action == parser.AddNotNull {
				err = scope.inheritanceOnlyCreatedConstraint(before, next)
			}
			if err != nil {
				return Result{}, err
			}
			scope.recordInheritanceAlterAction(before, next, action)
		}
		if pass == 0 {
			if definition != nil {
				if definition.afterDropOld != next {
					return Result{}, ids.ErrCatalogReplaced
				}
				if definition.afterDropError != nil {
					err = definition.afterDropError
				} else if definition.afterDrop != nil {
					next, err = scope.consumePreparedDrop(stage, definition.afterDrop, next, ref, changes)
				}
			} else {
				next, stage, err = e.stageDroppedConstraintIndexes(scope, auth, stage, old, next, s.Actions, changes)
			}
			if err == nil {
				next = releaseRetiredTypeConstraints(next, s.Actions)
			}
			if err == nil {
				pendingChecks, err = captureAlterTypeChecks(op, auth.Catalog, old, next, s.Actions)
			}
			if err == nil {
				pendingIndexes, err = captureAlterTypeIndexes(scope, auth.Catalog, old, next, s.Actions)
			}
		} else if pass == 1 {
			next, typeChecks, err = e.rebindAlterTypeChecks(scope, op, auth, ref, next, pendingChecks)
			if err == nil && len(pendingIndexes) > 0 {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.authorizeLocked(auth, op.session)
				}
				err = e.rebindAlterTypeIndexes(scope, auth, next, stage, pendingIndexes)
			}
		}
		if err == nil && pass == 1 && hasAttachments {
			if stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
			}
			err = stage.Replace(next)
			if definition != nil && !definition.phaseZeroOnly {
				if definition.usingCaptureOld != next {
					return Result{}, ids.ErrCatalogReplaced
				}
				if definition.usingCaptureError != nil {
					err = definition.usingCaptureError
				} else {
					attachments = definition.usingIndexes
				}
			} else {
				if err == nil {
					e.mu.Lock()
					err = e.evolveStageIndexesLocked(stage, ref.Address, transformations)
					e.mu.Unlock()
				}
				if err == nil {
					attachments, err = e.captureUsingIndexes(stage, next, s.Actions, transformations)
				}
			}
		}
		if err == nil && pass == 4 {
			inlineIndex := 0
			for _, added := range additions {
				if added.declaration.columnNotNull {
					before := next
					if definition != nil && !definition.phaseZeroOnly {
						if inlineIndex >= len(definition.inline) || definition.inline[inlineIndex].old != next {
							return Result{}, ids.ErrCatalogReplaced
						}
						preparedInline := definition.inline[inlineIndex]
						inlineIndex++
						if preparedInline.err != nil {
							err = preparedInline.err
						} else {
							next = preparedInline.next
						}
					} else {
						next, err = notNullCandidate(op, auth.Catalog, next, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.column.Name}, Constraint: parser.Identifier{Text: added.declaration.columnName}}, notNullDirect)
					}
					if err != nil {
						break
					}
					scope.recordInheritanceAlterAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
				}
			}
		}
		if err == nil && pass == 4 {
			for index, captured := range attachments {
				before := next
				if definition != nil && !definition.phaseZeroOnly {
					prepared := definition.usingNotNull[index]
					if prepared.old != next {
						err = ids.ErrCatalogReplaced
					} else if prepared.err != nil {
						err = prepared.err
					} else {
						next = prepared.next
					}
				} else {
					next, err = usingIndexNotNullCandidate(op, auth.Catalog, stageDefinitionReader{stage}, next, captured)
					if err == nil {
						err = scope.inheritanceOnlyCreatedConstraint(before, next)
					}
					if err == nil {
						attachments[index].notNullPrepared = true
					}
				}
				if err != nil {
					break
				}
				scope.recordInheritanceAlterAction(before, next, parser.AlterTableAction{Action: parser.AddNotNull})
			}
		}
		if err == nil && pass == 6 {
			var raw []*parser.CheckConstraint
			for _, action := range s.Actions {
				if action.Action == parser.AddCheck {
					raw = append(raw, action.Check)
				}
			}
			for _, added := range additions {
				for _, constraint := range added.column.Constraints {
					if constraint.Check != nil {
						raw = append(raw, constraint.Check)
					}
				}
			}
			if len(raw) > 1 {
				slices.SortFunc(raw, func(a, b *parser.CheckConstraint) int { return a.Pos.Offset - b.Pos.Offset })
			}
			if len(raw) != 0 && stage == nil {
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.Revision, auth.RoleRecord.Revision
			}
			next, err = e.stageAddedChecks(scope, op, auth, stage, next, raw, &addedChecks)
		}
		if err != nil {
			return Result{}, err
		}
	}
	op.definition = originalDefinition
	auth.definition = nil
	if definition != nil && next != old {
		next = detachPreparedInheritanceDefinition(next)
	}
	var storedReplacements []int32
	for _, attribute := range replacements {
		if next.ColumnMeta.Generations[attribute].Kind == catalog.GenerationStored {
			storedReplacements = append(storedReplacements, attribute)
		}
	}
	changedValues := len(transformations) != 0 || len(storedReplacements) != 0
	next = rowSecurityFlags(next, s.Actions)
	next, err = scope.finishInheritanceAlterCandidate(old, next)
	if err != nil {
		return Result{}, err
	}
	if !hasForeignKeys && len(transformations) == 0 && next == old && (stage == nil || stage.DescriptorCount() == 0 && stage.RemovalCount() == 0 && len(stage.sequence.stagedGenerators()) == 0) {
		if err := validateAlterTablespaceMove(destination); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
	if next != old {
		if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.ObjectRevision = old.Revision() + 1
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
		for address, descriptor := range stage.Descriptors() {
			sequence, ok := descriptor.(*catalog.Relation)
			if !ok || sequence.RelationKind != catalog.SequenceRelation {
				continue
			}
			original := grantCatalog.Get(address)
			if captured, identity := stage.sequence.identityOriginal(address); identity {
				original = captured
			}
			if e.catalog.state.Get(address) != original {
				e.mu.Unlock()
				return Result{}, catalog.Conflict{}
			}
			e.catalogChangeLocked(changes, address, descriptor)
			changes.generators[address] = stage.sequence.generator(address)
		}
		for address, removal := range stage.Removals() {
			sequence, ok := removal.Descriptor.(*catalog.Relation)
			if !ok || sequence.RelationKind != catalog.SequenceRelation {
				continue
			}
			original := grantCatalog.Get(address)
			if captured, identity := stage.sequence.identityOriginal(address); identity {
				original = captured
			}
			if e.catalog.state.Get(address) != original {
				e.mu.Unlock()
				return Result{}, catalog.Conflict{}
			}
			e.catalogChangeLocked(changes, address, nil)
			delete(changes.generators, address)
		}
	}
	scope.catalog = changes
	e.mu.Unlock()
	rewrite := len(storedReplacements) != 0
	for _, transformation := range transformations {
		rewrite = rewrite || transformation.rewrite
	}
	if next != old {
		next.ColumnMeta.Defaults = maps.Clone(next.ColumnMeta.Defaults)
		next.Constraints.Check = slices.Clone(next.Constraints.Check)
		next.Constraints.NotNull = slices.Clone(next.Constraints.NotNull)
		e.mu.Lock()
		err = e.assignDefinitionOIDsLocked(next)
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		if len(transformations) != 0 {
			if err := retireRebuiltCheckHistory(old, next); err != nil {
				return Result{}, err
			}
		}
	}
	if stage == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.authorizeLocked(auth, op.session)
	}
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
	next, err = e.rebuildAlterUniquesLocked(stage, next, transformations)
	if err == nil {
		next, err = e.rebuildAlterExclusionsLocked(stage, next, transformations)
	}
	if err == nil {
		err = e.evolveStageIndexesLocked(stage, ref.Address, transformations)
	}
	if err == nil {
		next, err = e.rebuildAlterForeignKeysLocked(stage, next, transformations)
	}
	if err == nil {
		next, err = rebindPhysicalNotNullType(stage, next, transformations)
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
		if fkIndex >= len(scope.foreignActionsOrZero().alterKeys) {
			return Result{}, ids.ErrCatalogReplaced
		}
		capture := scope.foreignActionsOrZero().alterKeys[fkIndex]
		next, err = e.stageAddedForeignKey(auth, stage, next, action.ForeignKey, capture.target.Address, capture.index)
		if err != nil {
			return Result{}, err
		}
		addedForeignKeys = append(addedForeignKeys, next.Constraints.ForeignKeys[len(next.Constraints.ForeignKeys)-1].OID)
		fkIndex++
	}
	if hasForeignKeys && next.Revision() == old.Revision() {
		if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.ObjectRevision = old.Revision() + 1
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
		e.mu.Lock()
		err = e.captureCheckCommentSnapshotLocked(scope, next)
		if err == nil {
			err = e.captureCheckVisibilitySnapshotLocked(scope, next)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	for _, added := range additions {
		if next.ColumnMeta.Defaults[added.attribute].CatalogSnapshot {
			e.mu.Lock()
			err = e.captureCommentSnapshotLocked(scope)
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
			break
		}
	}
	fills, perRowAdd, err := planAddedColumns(scope, additions)
	if err != nil {
		return Result{}, err
	}
	rowRewrite := changedValues || perRowAdd
	for _, fill := range fills {
		rowRewrite = rowRewrite || !fill.IsNull()
	}
	if rowRewrite {
		if err := typedef.RowRewriteError(auth.Catalog, old); err != nil {
			return Result{}, err
		}
	}
	rewrite = rewrite || perRowAdd
	if rewrite {
		scope.owner.mu.Lock()
		err = scope.acquireSerializableSnapshot()
		if err == nil {
			e.mu.Lock()
			err = e.serializableReadLocked(scope, ref)
			e.mu.Unlock()
		}
		scope.owner.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	storageOwner := old
	if accumulated, exists := changes.storage[ref.Address]; exists {
		shape = accumulated
		store = accumulated.replacement
		storageOwner = accumulated.relation
		versions, ends = store.Versions, store.Ends
	}
	if len(fills) != 0 || len(shape.fills) != 0 {
		shape.fills = maps.Clone(shape.fills)
		if shape.fills == nil {
			shape.fills = make(map[int32]sqltype.Value, len(fills))
		}
		for attribute, value := range fills {
			shape.fills[attribute] = value
		}
		for attribute := range shape.fills {
			if !next.HasAttribute(attribute) {
				delete(shape.fills, attribute)
			}
		}
	}
	replacement := store
	projectedWrites, projectedDeletes := scope.columnProjectionRoots(ref.Address)
	validator := newAlterRowValidator(next, schema)
	if len(storedReplacements) != 0 {
		typeChecks = validCheckTraversal(next).Constraints.Check
	}
	if len(typeChecks) != 0 {
		traversal := *next
		traversal.Constraints.Check = typeChecks
		validator.checks = &traversal
		e.mu.Lock()
		err = e.captureCheckCommentSnapshotLocked(scope, &traversal)
		if err == nil {
			err = e.captureCheckVisibilitySnapshotLocked(scope, &traversal)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	if rewrite {
		// A rewrite creates the destination heap before evaluating old rows.
		if err := validateAlterTablespaceMove(destination); err != nil {
			return Result{}, err
		}
		replacement, projectedWrites, projectedDeletes, err = rewriteTypeRows(scope, op, storageOwner, next, store, frontier, transformations, additions, storedReplacements, validator)
		if err != nil {
			return Result{}, err
		}
		versions, ends = replacement.Versions, replacement.Ends
		shape.rewritten, shape.frontier = true, frontier
		shape.snapshot, shape.hasSnapshot = nil, false
	} else if !slices.Equal(storageOwner.Attributes, next.Attributes) {
		shape, projectedWrites, projectedDeletes, err = projectColumnShape(scope, storageOwner, next, store, shape, snapshot, snapshotExists)
		if err != nil {
			return Result{}, err
		}
		replacement = shape.replacement
		versions, ends = replacement.Versions, replacement.Ends
	}
	for _, action := range s.Actions {
		if action.Action != parser.DropConstraint {
			continue
		}
		for _, c := range old.Constraints.Unique {
			if c.Name == action.Constraint.Text {
				if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
					return Result{}, err
				}
			}
		}
	}
	// Recapture effective authority after provider waits and row rewriting.
	// The immutable snapshot is used outside metadata locks for disclosure.
	e.mu.Lock()
	err = scope.validateLockedRelation(e)
	var disclosure AuthorizationView
	if err == nil {
		disclosure, err = e.authorizationLocked(op)
		disclosure.Catalog = e.privateCatalogLocked(scope.owner.catalog)
		disclosure.Recapture(disclosure.Catalog)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	indexEvaluation := EvalContext{evalStatement: evalStatement{Context: op.Context(), statementScope: scope, Input: scope.input, Output: scope.output, catalog: e.stageRead(stage)}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	indexBuilds, err := validateAlterIndexBuilds(&indexEvaluation, disclosure, e.stageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.output, transformations, storedReplacements, addedIndexes, rewrite)
	if err != nil {
		return Result{}, err
	}
	var indexOutputRows bool
	projectedWrites, indexOutputRows, err = attachIndexBuildOutputs(op.Context(), projectedWrites, indexBuilds)
	if err != nil {
		return Result{}, err
	}
	scanChecks := addedChecks
	if !rewrite {
		scanChecks = append(typeChecks, addedChecks...)
	}
	virtualNotNullScan := false
	if !rewrite {
		for _, attribute := range replacements {
			if next.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual && slices.Contains(validator.notNull, next.IndexForAttribute(attribute)) {
				virtualNotNullScan = true
				break
			}
		}
	}
	if len(scanChecks) != 0 || !rewrite && (needsNotNullScan(old, next) || virtualNotNullScan) {
		scanValidator := newAlterRowValidator(next, schema)
		if rewrite {
			scanValidator.notNull = nil
		}
		if len(scanChecks) != 0 {
			traversal := *next
			traversal.Constraints.Check = scanChecks
			scanValidator.checks = &traversal
			e.mu.Lock()
			err = e.captureCheckCommentSnapshotLocked(scope, &traversal)
			if err == nil {
				err = e.captureCheckVisibilitySnapshotLocked(scope, &traversal)
			}
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
		}
		if err := scope.validateAddedConstraintRows(scanValidator, versions, ends, frontier, projectedWrites, projectedDeletes); err != nil {
			return Result{}, err
		}
	}
	// Without a rewrite, old-row CHECK/NOT NULL validation precedes movement.
	if err := validateAlterTablespaceMove(destination); err != nil {
		return Result{}, err
	}
	var dependencyTargets []ids.ObjectAddress
	for _, attribute := range old.Attributes {
		if !next.HasAttribute(attribute) {
			target := ref.Address
			target.SubID = attribute
			dependencyTargets = append(dependencyTargets, target)
		}
	}
	for _, transformation := range transformations {
		target := ref.Address
		target.SubID = transformation.attribute
		dependencyTargets = append(dependencyTargets, target)
	}
	for _, attribute := range replacements {
		target := ref.Address
		target.SubID = attribute
		dependencyTargets = append(dependencyTargets, target)
	}
	for a := range stage.Removals() {
		if _, identity := stage.sequence.identityOriginal(a); identity {
			continue
		}
		dependencyTargets = append(dependencyTargets, a)
	}
	if len(dependencyTargets) != 0 {
		changes.dependencies = append(changes.dependencies, catalogDependencyObservation{grantCatalog, dependencyTargets})
	}
	changes.dependencies = append(changes.dependencies, stage.sequence.identityDependencyList()...)
	if scope.inheritanceAlter == nil {
		if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
			return Result{}, err
		}
	}
	// Validation closes the type graph before transferring it to private catalog
	// changes. Include dependent holder stores as well as the directly altered row.
	for address, rebound := range stage.rowShapes {
		consumer := stage.Get(address).(*catalog.Relation)
		changes.storage[address] = catalogStorageChange{original: rebound.original, root: rebound.original.Versions, ends: rebound.observedEnds, generation: rebound.generation, replacement: rebound.replacement, relation: consumer}
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
	for a, d := range stage.Descriptors() {
		original := grantCatalog.Get(a)
		if captured, identity := stage.sequence.identityOriginal(a); identity {
			original = captured
		}
		if e.catalog.state.Get(a) != original {
			e.mu.Unlock()
			return Result{}, catalog.Conflict{}
		}
		e.catalogChangeLocked(changes, a, d)
	}
	for a := range stage.Removals() {
		original := grantCatalog.Get(a)
		if captured, identity := stage.sequence.identityOriginal(a); identity {
			original = captured
		}
		if e.catalog.state.Get(a) != original {
			e.mu.Unlock()
			return Result{}, catalog.Conflict{}
		}
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.generators, a)
	}
	for a, generator := range stage.sequence.stagedGenerators() {
		if _, removed := stage.Removal(a); !removed {
			changes.generators[a] = generator
		}
	}
	if shape.replacement != replacement {
		shape.truncated = false
	}
	shape.replacement, shape.relation = replacement, next
	metadataOnly := !changedValues && len(additions) == 0 && slices.Equal(old.Attributes, next.Attributes)
	// A storage entry requests index reconstruction at publication. Attachment
	// metadata needs none; retain any earlier private shape without restamping it.
	if !hasAttachments || !metadataOnly {
		changes.storage[ref.Address] = shape
	}
	e.mu.Unlock()
	if !rewrite {
		sourceWrites, _ := scope.columnProjectionRoots(ref.Address)
		var transported bool
		projectedWrites, transported, err = transportIndexLayouts(&indexEvaluation, stage, changes, next, store, replacement, sourceWrites, projectedWrites)
		if err != nil {
			return Result{}, err
		}
		indexOutputRows = indexOutputRows || transported
	}
	if err := prepareAlterIndexRoots(&indexEvaluation, changes, stage.Base, next, replacement, indexBuilds, metadataOnly); err != nil {
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
		if err := scope.validateAlterForeignKeys(e.stageRead(stage), ref.Address); err != nil {
			return Result{}, err
		}
	}
	if len(transformations) == 0 {
		if err := scope.validateAddedForeignKeys(e.stageRead(stage), next, addedForeignKeys); err != nil {
			return Result{}, err
		}
	}
	if generators := stage.sequence.stagedGenerators(); len(generators) != 0 {
		e.mu.Lock()
		for address, generator := range generators {
			if memory := op.session.sequences.Memory(address); memory != nil {
				memory.DiscardStaleTailLocked(generator.Generation)
			}
		}
		e.mu.Unlock()
	}
	if rewrite {
		scope.owner.mu.Lock()
		e.mu.Lock()
		err = scope.validateLockedRelation(e)
		if err == nil {
			event := [1]visibility.SerializableWrite{{Rewrite: true}}
			err = e.serializableWriteLocked(scope, ref, event[:])
		}
		e.mu.Unlock()
		scope.owner.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	scope.catalog = changes
	if scope.inheritanceAlter == nil {
		emitViewCascade(op, stage.viewCascadeNames)
	}
	return Result{}, nil
}
