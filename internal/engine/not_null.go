package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// AX protects the captured incarnation; candidates remain private until Finish.
// The caller holds Engine.mu. Every return releases it before callbacks/scanning.
func (e *Engine) alterTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTable, count int) (Result, error) {
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
		err = ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*relationDescriptor)
	if hierarchyRelation(old) && partitionConstraintDropActions(old, s.Actions) {
		return e.executePartitionConstraintDropLocked(scope, auth, old, s)
	}
	if len(s.Actions) == 0 {
		return fail(fmt.Errorf("stuntgres: empty ALTER TABLE"))
	}
	if err := typedDirectActionsGuard(old, s.Actions); err != nil {
		return fail(err)
	}
	for _, action := range s.Actions {
		label := "ALTER COLUMN ... SET NOT NULL"
		switch action.Action {
		case parser.DropNotNull:
			label = "ALTER COLUMN ... DROP NOT NULL"
		case parser.AddNotNull, parser.AddExclusion, parser.AddUsingIndex:
			label = "ADD CONSTRAINT"
		case parser.DropConstraint:
			label = "DROP CONSTRAINT"
		case parser.DropColumn:
			label = "DROP COLUMN"
		case parser.SetNotNull:
		case parser.AddIdentity, parser.DropIdentity, parser.SetIdentity:
			label = "ALTER COLUMN"
		case parser.AlterColumnType:
			label = "ALTER COLUMN ... TYPE"
		case parser.AddColumn:
			label = "ADD COLUMN"
		case parser.SetExpression, parser.DropExpression:
			label = "ALTER COLUMN"
		case parser.EnableRowSecurity, parser.DisableRowSecurity, parser.ForceRowSecurity, parser.NoForceRowSecurity:
			if err := rowSecurityKind(old, action.Action); err != nil {
				return fail(err)
			}
			continue
		default:
			return fail(fmt.Errorf("stuntgres: invalid ALTER action"))
		}
		if err := tableKind(old, label); err != nil {
			return fail(err)
		}
	}
	if err := inheritanceAlterActionsGuard(auth.catalog, old, s.Actions); err != nil {
		return fail(err)
	}
	if rowSecurityActions(s.Actions) {
		return e.stageRowSecurityFlagsLocked(scope, op, auth, old, rowSecurityFlags(old, s.Actions))
	}
	changes := cloneCatalogChanges(scope.owner.catalog)
	original := e.storage[ref.Address]
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
		shape = catalogStorageChange{original: original, root: original.versions, ends: original.ends, generation: original.generation, replacement: store, relation: old}
	}
	versions, ends, frontier := store.versions, store.ends, e.commitSeq
	snapshot, snapshotExists := scope.owner.roots[ref.Address]
	grantCatalog := e.state
	schema := auth.catalog.state.descriptors[old.namespace].Name()
	var stage *CatalogStage
	scope.catalogAuthority = &catalogAuthority{session: op.session, authorization: auth.revision, roles: auth.roles.state.revision, ref: ref, descriptor: e.state.descriptors[ref.Address]}
	e.mu.Unlock()
	defer func() { e.DiscardCatalogStage(stage) }()
	prepared, preparedStage, err := e.prepareAlterIdentityTypes(scope, op, auth, old, ref, s.Actions)
	stage = preparedStage
	if err != nil {
		return Result{}, err
	}
	next := old
	var transformations []columnTransformation
	var pendingChecks []alterTypeCheck
	var pendingIndexes []alterTypeIndex
	var typeChecks []checkConstraint
	var additions []addedColumn
	var replacements []int32
	var attachments []usingIndexAttachment
	hasAttachments := false
	for _, action := range s.Actions {
		hasAttachments = hasAttachments || action.Action == parser.AddUsingIndex
	}
	// PostgreSQL orders DROP, TYPE, ADD columns, generated SET, nullability,
	// constraints, then identity ADD/SET. Each pass retains source list order.
	for pass := range 8 {
		typeIndex := 0
		attachmentIndex := 0
		for _, action := range s.Actions {
			selected := pass == 0 && (action.Action == parser.DropIdentity || action.Action == parser.DropNotNull || action.Action == parser.DropConstraint || action.Action == parser.DropColumn || action.Action == parser.DropExpression) || pass == 1 && action.Action == parser.AlterColumnType || pass == 2 && action.Action == parser.AddColumn || pass == 3 && action.Action == parser.SetExpression || pass == 4 && action.Action == parser.SetNotNull || pass == 5 && (action.Action == parser.AddNotNull || action.Action == parser.AddExclusion || action.Action == parser.AddUsingIndex) || pass == 6 && action.Action == parser.AddIdentity || pass == 7 && action.Action == parser.SetIdentity
			if !selected {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if action.Action == parser.AddIdentity || action.Action == parser.DropIdentity || action.Action == parser.SetIdentity {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
					stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
				}
				next, err = e.identityCandidate(scope, auth, stage, next, action)
			} else if action.Action == parser.SetExpression || action.Action == parser.DropExpression {
				var attribute int32
				next, attribute, err = e.generationCandidate(scope, op, auth, next, action)
				if err == nil && attribute != 0 && !slices.Contains(replacements, attribute) {
					replacements = append(replacements, attribute)
				}
			} else if action.Action == parser.DropColumn {
				if _, exists := next.index[action.Column.Text]; exists && stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
					stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
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
				var captured *alterTypePreparation
				if prepared != nil {
					captured = &prepared[typeIndex]
					typeIndex++
				}
				next, transformation, err = e.alterTypeCandidate(scope, op, auth, stage, old, next, ref, action, captured)
				if err == nil {
					transformations = append(transformations, transformation)
				}
			} else if action.Action == parser.AddUsingIndex {
				next, err = e.stageUsingIndex(op, stage, next, attachments[attachmentIndex])
				attachmentIndex++
			} else if action.Action == parser.AddExclusion {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
					stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
				}
				copy := *next
				copy.exclusionConstraints = slices.Clone(next.exclusionConstraints)
				next = &copy
				e.mu.Lock()
				err = e.stageExclusion(op, scope.input, auth, stage, next, action.Exclusion)
				e.mu.Unlock()
			} else {
				next, err = notNullCandidate(op, auth.catalog, next, action)
			}
			if err != nil {
				return Result{}, err
			}
		}
		if pass == 0 {
			pendingChecks, err = captureAlterTypeChecks(op, auth.catalog, old, next, s.Actions)
			if err == nil {
				pendingIndexes, err = captureAlterTypeIndexes(scope, auth.catalog, old, next, s.Actions)
			}
		} else if pass == 1 {
			next, typeChecks, err = e.rebindAlterTypeChecks(scope, op, auth, ref, next, pendingChecks)
			if err == nil && len(pendingIndexes) > 0 {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
					stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
				}
				err = e.rebindAlterTypeIndexes(scope, auth, next, stage, pendingIndexes)
			}
		}
		if err == nil && pass == 1 && hasAttachments {
			if stage == nil {
				stage = e.BeginCatalogStage(auth.catalog)
			}
			err = stage.Replace(next)
			if err == nil {
				e.mu.Lock()
				err = e.evolveStageIndexesLocked(stage, ref.Address, transformations)
				e.mu.Unlock()
			}
			if err == nil {
				attachments, err = e.captureUsingIndexes(stage, next, s.Actions, transformations)
			}
		}
		if err == nil && pass == 4 {
			for _, added := range additions {
				if added.declaration.columnNotNull {
					next, err = notNullCandidate(op, auth.catalog, next, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.column.Name}, Constraint: parser.Identifier{Text: added.declaration.columnName}})
					if err != nil {
						break
					}
				}
			}
		}
		if err == nil && pass == 5 {
			var raw []*parser.CheckConstraint
			for _, added := range additions {
				for _, constraint := range added.column.Constraints {
					if constraint.Check != nil {
						raw = append(raw, constraint.Check)
					}
				}
			}
			before := make(map[int]bool, len(next.checkConstraints))
			for _, check := range next.checkConstraints {
				before[check.ordinal] = true
			}
			err = e.bindColumnChecks(op, auth, raw, next, scope.input, true, nil)
			if err == nil {
				for _, check := range orderedChecks(next) {
					if !before[check.ordinal] {
						typeChecks = append(typeChecks, check)
					}
				}
			}
		}
		if err != nil {
			return Result{}, err
		}
	}
	var storedReplacements []int32
	for _, attribute := range replacements {
		if next.generations[attribute].kind == generationStored {
			storedReplacements = append(storedReplacements, attribute)
		}
	}
	changedValues := len(transformations) != 0 || len(storedReplacements) != 0
	next = rowSecurityFlags(next, s.Actions)
	if len(transformations) == 0 && next == old && (stage == nil || len(stage.descriptors) == 0 && len(stage.removals) == 0 && len(stage.generators) == 0) {
		return Result{}, nil
	}
	if next != old {
		if old.revision == DefinitionRevision(^uint64(0)) {
			return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next.revision = old.revision + 1
	}
	// USING/CHECK demand must allocate from the AS replacement, not the live
	// generator. This statement overlay is discarded on failure, never installed
	// in the owner before Finish. Keep the exact generator pointers through merge.
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	if stage != nil {
		for address, descriptor := range stage.descriptors {
			sequence, ok := descriptor.(*relationDescriptor)
			if !ok || sequence.relationKind != sequenceRelation {
				continue
			}
			original := grantCatalog.descriptors[address]
			if captured, identity := stage.identityOriginals[address]; identity {
				original = captured
			}
			if e.state.descriptors[address] != original {
				e.mu.Unlock()
				return Result{}, CatalogConflict{}
			}
			e.catalogChangeLocked(changes, address, descriptor)
			changes.generators[address] = stage.generators[address]
		}
		for address, removal := range stage.removals {
			sequence, ok := removal.descriptor.(*relationDescriptor)
			if !ok || sequence.relationKind != sequenceRelation {
				continue
			}
			original := grantCatalog.descriptors[address]
			if captured, identity := stage.identityOriginals[address]; identity {
				original = captured
			}
			if e.state.descriptors[address] != original {
				e.mu.Unlock()
				return Result{}, CatalogConflict{}
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
		next.defaults = maps.Clone(next.defaults)
		next.checkConstraints = slices.Clone(next.checkConstraints)
		next.notNullConstraints = slices.Clone(next.notNullConstraints)
		e.mu.Lock()
		err = e.assignDefinitionOIDsLocked(next)
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	if stage == nil {
		stage = e.BeginCatalogStage(auth.catalog)
		stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	}
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	if changedValues {
		if err := scope.acquireForeignKeyTypeProviders(auth.catalog, old.address); err != nil {
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
		if next.defaults[added.attribute].catalogSnapshot {
			e.mu.Lock()
			err = e.captureCommentSnapshotLocked(scope)
			e.mu.Unlock()
			if err != nil {
				return Result{}, err
			}
			break
		}
	}
	fills, volatileAdd, err := planAddedColumns(scope, additions)
	if err != nil {
		return Result{}, err
	}
	rowRewrite := changedValues || volatileAdd
	for _, fill := range fills {
		rowRewrite = rowRewrite || !fill.IsNull()
	}
	if rowRewrite {
		if err := rowRewriteError(auth.catalog, old); err != nil {
			return Result{}, err
		}
	}
	rewrite = rewrite || volatileAdd
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
		versions, ends = store.versions, store.ends
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
			if !next.hasAttribute(attribute) {
				delete(shape.fills, attribute)
			}
		}
	}
	replacement := store
	projectedWrites, projectedDeletes := scope.columnProjectionRoots(ref.Address)
	validator := alterRowValidator{relation: next, schema: schema}
	if len(storedReplacements) != 0 {
		typeChecks = next.checkConstraints
	}
	if len(typeChecks) != 0 {
		traversal := *next
		traversal.checkConstraints = typeChecks
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
		replacement, projectedWrites, projectedDeletes, err = rewriteTypeRows(scope, op, storageOwner, next, store, frontier, transformations, additions, storedReplacements, validator)
		if err != nil {
			return Result{}, err
		}
		versions, ends = replacement.versions, replacement.ends
		shape.rewritten, shape.frontier = true, frontier
		shape.snapshot, shape.hasSnapshot = nil, false
	} else if !slices.Equal(storageOwner.attributes, next.attributes) {
		shape, projectedWrites, projectedDeletes, err = projectColumnShape(scope, storageOwner, next, store, shape, snapshot, snapshotExists)
		if err != nil {
			return Result{}, err
		}
		replacement = shape.replacement
		versions, ends = replacement.versions, replacement.ends
	}
	needsScan := !rewrite && len(typeChecks) != 0
	if !rewrite {
		for slot, column := range next.columns {
			oldSlot := slices.Index(old.attributes, next.attributes[slot])
			if !column.Nullable && (oldSlot < 0 || old.columns[oldSlot].Nullable) {
				needsScan = true
				break
			}
		}
	}
	if !rewrite {
		for _, attribute := range replacements {
			slot := next.indexForAttribute(attribute)
			if next.generations[attribute].kind == generationVirtual && !next.columns[slot].Nullable {
				needsScan = true
				break
			}
		}
	}
	// Ignore committed predecessors replaced by own writes, and all ended rows.
	var replaced map[RowID]bool
	if needsScan {
		for _, row := range projectedWrites {
			if row.base != 0 {
				if replaced == nil {
					replaced = make(map[RowID]bool)
				}
				replaced[row.row] = true
			}
		}
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
	scan := func(rows []rowVersion, committed bool) error {
		for _, row := range rows {
			if err := op.Context().Err(); err != nil {
				return err
			}
			if committed && replaced[row.row] || !liveVersion(row, frontier, ends, projectedDeletes) {
				continue
			}
			if err := validator.validate(row.values, &evaluation); err != nil {
				return err
			}
		}
		return nil
	}
	if needsScan {
		if err := scan(versions, true); err != nil {
			return Result{}, err
		}
		if err := scan(projectedWrites, false); err != nil {
			return Result{}, err
		}
	}
	// Final rebound predicates and reconstructed attachments must exist before
	// TYPE validates the effective committed/private row image.
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
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
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	for _, action := range s.Actions {
		if action.Action != parser.DropConstraint {
			continue
		}
		for _, c := range old.uniqueConstraints {
			if c.Name == action.Constraint.Text {
				if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
					return Result{}, err
				}
				if _, removed := stage.removals[c.Index]; removed {
					if err := scope.stageExpressionDependents(stage, []ObjectAddress{c.Index}, action.Mode, changes); err != nil {
						return Result{}, err
					}
				}
			}
		}
	}
	var uniqueBaselines map[string]*logicalIndexRoot
	if changedValues {
		// Recapture effective authority after provider waits and row rewriting.
		// The immutable snapshot is used outside metadata locks for disclosure.
		e.mu.Lock()
		err = scope.validateLockedRelation(e)
		var disclosure AuthorizationView
		if err == nil {
			disclosure, err = e.authorizationLocked(op)
			disclosure.catalog = e.privateCatalogLocked(scope.owner.catalog)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		uniqueBaselines, err = validateAlterUniques(op.Context(), disclosure, e.stageRead(stage), next, replacement, projectedWrites, projectedDeletes, schema, scope.output, transformations, storedReplacements)
		if err != nil {
			return Result{}, err
		}
	}
	addedBaselines, err := validateAddedExclusions(op.Context(), auth, e.stageRead(stage), old, next, replacement, projectedWrites, projectedDeletes, schema, scope.output)
	if err != nil {
		return Result{}, err
	}
	if addedBaselines != nil {
		if uniqueBaselines == nil {
			uniqueBaselines = make(map[string]*logicalIndexRoot)
		}
		for name, root := range addedBaselines {
			uniqueBaselines[name] = root
		}
	}
	var dependencyTargets []ObjectAddress
	for _, attribute := range old.attributes {
		if !next.hasAttribute(attribute) {
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
	for a := range stage.removals {
		if _, identity := stage.identityOriginals[a]; identity {
			continue
		}
		dependencyTargets = append(dependencyTargets, a)
	}
	if len(dependencyTargets) != 0 {
		changes.dependencies = append(changes.dependencies, catalogDependencyObservation{grantCatalog, dependencyTargets})
	}
	changes.dependencies = append(changes.dependencies, stage.identityDependencies...)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	// Validation closes the type graph before transferring it to private catalog
	// changes. Include dependent holder stores as well as the directly altered row.
	for address, rebound := range stage.rowShapes {
		consumer := stage.descriptors[address].(*relationDescriptor)
		changes.storage[address] = catalogStorageChange{original: rebound.original, root: rebound.original.versions, ends: rebound.observedEnds, generation: rebound.generation, replacement: rebound.replacement, relation: consumer}
	}
	// Keep the pre-callback originals; Reset cannot restamp captured authority.
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	if hasAttachments {
		for name := range stage.touched {
			if e.state.names[name] != grantCatalog.names[name] {
				e.mu.Unlock()
				return Result{}, CatalogConflict{}
			}
		}
	}
	for a, d := range stage.descriptors {
		original := grantCatalog.descriptors[a]
		if captured, identity := stage.identityOriginals[a]; identity {
			original = captured
		}
		if e.state.descriptors[a] != original {
			e.mu.Unlock()
			return Result{}, CatalogConflict{}
		}
		e.catalogChangeLocked(changes, a, d)
	}
	for a := range stage.removals {
		original := grantCatalog.descriptors[a]
		if captured, identity := stage.identityOriginals[a]; identity {
			original = captured
		}
		if e.state.descriptors[a] != original {
			e.mu.Unlock()
			return Result{}, CatalogConflict{}
		}
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.generators, a)
	}
	for a, generator := range stage.generators {
		if _, removed := stage.removals[a]; !removed {
			changes.generators[a] = generator
		}
	}
	if shape.replacement != replacement {
		shape.truncated = false
	}
	shape.replacement, shape.relation = replacement, next
	metadataOnly := !changedValues && len(additions) == 0 && slices.Equal(old.attributes, next.attributes)
	// A storage entry requests index reconstruction at publication. Attachment
	// metadata needs none; retain any earlier private shape without restamping it.
	if !hasAttachments || !metadataOnly {
		changes.storage[ref.Address] = shape
	}
	e.mu.Unlock()
	if err := prepareAlterUniqueRoots(op.Context(), changes, stage.base, next, replacement, uniqueBaselines, metadataOnly, storedReplacements); err != nil {
		return Result{}, err
	}
	if err := scope.prepareExclusionShape(old, next, e.stageRead(stage), projectedWrites, projectedDeletes); err != nil {
		return Result{}, err
	}
	// A shape statement replaces prior own roots only on the successful merge.
	if replacement != store {
		scope.stageProjectedColumnRoots(ref.Address, projectedWrites, projectedDeletes)
	}
	if changedValues {
		if err := scope.validateAlterForeignKeys(e.stageRead(stage), ref.Address); err != nil {
			return Result{}, err
		}
	}
	if len(stage.generators) != 0 {
		e.mu.Lock()
		for address, generator := range stage.generators {
			if memory := op.session.sequences[address]; memory != nil && memory.generation != generator.generation {
				memory.remaining = 0
			}
		}
		e.mu.Unlock()
	}
	if rewrite {
		scope.owner.mu.Lock()
		e.mu.Lock()
		err = scope.validateLockedRelation(e)
		if err == nil {
			event := [1]serializableWrite{{rewrite: true}}
			err = e.serializableWriteLocked(scope, ref, event[:])
		}
		e.mu.Unlock()
		scope.owner.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
	}
	scope.catalog = changes
	emitViewCascade(op, stage.viewCascadeNames)
	return Result{}, nil
}

func notNullCandidate(op *Operation, catalog CatalogRead, old *relationDescriptor, a parser.AlterTableAction) (*relationDescriptor, error) {
	slot, index := -1, -1
	if a.Action == parser.DropConstraint {
		for i, c := range old.notNullConstraints {
			if c.Name == a.Constraint.Text {
				index = i
				for j, attr := range old.attributes {
					if attr == c.Attribute {
						slot = j
						break
					}
				}
				break
			}
		}
		for _, c := range old.checkConstraints {
			if c.Name == a.Constraint.Text {
				next := *old
				next.checkConstraints = make([]checkConstraint, 0, len(old.checkConstraints)-1)
				for _, constraint := range old.checkConstraints {
					if constraint.Name != a.Constraint.Text {
						next.checkConstraints = append(next.checkConstraints, constraint)
					}
				}
				return &next, nil
			}
		}
		for i, c := range old.uniqueConstraints {
			if c.Name == a.Constraint.Text {
				if c.Parent != 0 {
					return nil, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, old.name))
				}
				index, exists := catalog.state.descriptors[c.Index].(*relationDescriptor)
				if !exists {
					return nil, ErrCatalogReplaced
				}
				if err := partitionIndexDropDependency(catalog, index); err != nil {
					return nil, err
				}
				next := *old
				next.uniqueConstraints = make([]uniqueConstraint, 0, len(old.uniqueConstraints)-1)
				next.uniqueConstraints = append(next.uniqueConstraints, old.uniqueConstraints[:i]...)
				next.uniqueConstraints = append(next.uniqueConstraints, old.uniqueConstraints[i+1:]...)
				return &next, nil
			}
		}
		for i, c := range old.exclusionConstraints {
			if c.Name == a.Constraint.Text {
				next := *old
				next.exclusionConstraints = make([]exclusionConstraint, 0, len(old.exclusionConstraints)-1)
				next.exclusionConstraints = append(next.exclusionConstraints, old.exclusionConstraints[:i]...)
				next.exclusionConstraints = append(next.exclusionConstraints, old.exclusionConstraints[i+1:]...)
				return &next, nil
			}
		}
		for i, c := range old.foreignKeys {
			if c.Name == a.Constraint.Text {
				next := *old
				next.foreignKeys = make([]foreignKeyConstraint, 0, len(old.foreignKeys)-1)
				next.foreignKeys = append(next.foreignKeys, old.foreignKeys[:i]...)
				next.foreignKeys = append(next.foreignKeys, old.foreignKeys[i+1:]...)
				return &next, nil
			}
		}
		if slot < 0 {
			if !a.IfExists {
				return nil, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, a.Constraint.Text, old.name))
			}
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist, skipping`, a.Constraint.Text, old.name)})
			return old, nil
		}
	} else {
		if isSystemColumn(a.Column.Text) {
			message := "system-column nullability is unsupported"
			if a.Action == parser.AddNotNull {
				message = fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, a.Column.Text)
			} else if a.Action == parser.DropNotNull {
				message = fmt.Sprintf(`cannot alter system column "%s"`, a.Column.Text)
			}
			return nil, sqlError("0A000", message)
		}
		var ok bool
		slot, ok = old.index[a.Column.Text]
		if !ok {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.name))
		}
		for i, c := range old.notNullConstraints {
			if c.Attribute == old.attributes[slot] {
				index = i
				break
			}
		}
	}
	adding := a.Action == parser.SetNotNull || a.Action == parser.AddNotNull
	if !adding {
		_, identity := old.identities[old.attributes[slot]]
		// ALTER COLUMN checks identity before entering constraint removal.
		// Named DROP reaches the primary-key protection first instead.
		if identity && a.Action != parser.DropConstraint {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.columns[slot].Name, old.name))
		}
		for _, constraint := range old.uniqueConstraints {
			if constraint.Primary && slices.Contains(constraint.Attributes, old.attributes[slot]) {
				return nil, sqlError("42P16", fmt.Sprintf(`column "%s" is in a primary key`, old.columns[slot].Name))
			}
		}
		if identity {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.columns[slot].Name, old.name))
		}
	}
	if adding && index >= 0 || !adding && index < 0 {
		return old, nil
	}
	name := a.Constraint.Text
	if adding {
		if name == "" {
			name = generatedConstraintName(old.name, old.columns[slot].Name, "not_null", constraintNames(catalog, old.namespace, old))
		}
		if relationConstraintNameExists(old, name) {
			return nil, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.name))
		}
	}
	next := *old
	next.columns = append([]ColumnDescriptor(nil), old.columns...)
	next.columns[slot].Nullable = !adding
	if adding {
		next.notNullConstraints = make([]notNullConstraint, len(old.notNullConstraints)+1)
		copy(next.notNullConstraints, old.notNullConstraints)
		next.notNullConstraints[len(old.notNullConstraints)] = notNullConstraint{Name: name, Attribute: old.attributes[slot]}
	} else {
		next.notNullConstraints = make([]notNullConstraint, 0, len(old.notNullConstraints)-1)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[:index]...)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[index+1:]...)
	}
	return &next, nil
}
