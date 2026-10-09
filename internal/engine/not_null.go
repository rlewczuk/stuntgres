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
	ref, err := e.resolveReference(auth, s.Name)
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
	if len(s.Actions) == 0 {
		return fail(fmt.Errorf("stuntgres: empty ALTER TABLE"))
	}
	for _, action := range s.Actions {
		label := "ALTER COLUMN ... SET NOT NULL"
		switch action.Action {
		case parser.DropNotNull:
			label = "ALTER COLUMN ... DROP NOT NULL"
		case parser.AddNotNull:
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
		default:
			return fail(fmt.Errorf("stuntgres: invalid ALTER action"))
		}
		if err := tableKind(old, label); err != nil {
			return fail(err)
		}
	}
	changes := scope.owner.catalog
	original := e.storage[ref.Address]
	store := e.privateStorageLocked(changes, ref.Address)
	if store == nil || original == nil {
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
	var typeChecks []checkConstraint
	var additions []addedColumn
	// PostgreSQL orders DROP, TYPE, ADD columns, original SET, constraints,
	// then identity ADD/SET. Added CHECKs bind against the completed ADD shape.
	for pass := range 7 {
		typeIndex := 0
		for _, action := range s.Actions {
			selected := pass == 0 && (action.Action == parser.DropIdentity || action.Action == parser.DropNotNull || action.Action == parser.DropConstraint || action.Action == parser.DropColumn) || pass == 1 && action.Action == parser.AlterColumnType || pass == 2 && action.Action == parser.AddColumn || pass == 3 && action.Action == parser.SetNotNull || pass == 4 && action.Action == parser.AddNotNull || pass == 5 && action.Action == parser.AddIdentity || pass == 6 && action.Action == parser.SetIdentity
			if !selected {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if action.Action == parser.AddIdentity || action.Action == parser.DropIdentity || action.Action == parser.SetIdentity {
				if stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
				}
				next, err = e.identityCandidate(scope, auth, stage, next, action)
			} else if action.Action == parser.DropColumn {
				if _, exists := next.index[action.Column.Text]; exists && stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
				}
				next, err = scope.dropColumnCandidate(stage, next, ref, action)
			} else if action.Action == parser.AddColumn {
				var added addedColumn
				next, added, err = e.addColumnCandidate(scope, op, auth, next, action)
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
			} else {
				next, err = notNullCandidate(op, auth.catalog, next, action)
			}
			if err != nil {
				return Result{}, err
			}
		}
		if pass == 0 {
			pendingChecks, err = captureAlterTypeChecks(op, auth.catalog, old, next, s.Actions)
		} else if pass == 1 {
			next, typeChecks, err = e.rebindAlterTypeChecks(scope, op, auth, ref, next, pendingChecks)
		}
		if err == nil && pass == 3 {
			for _, added := range additions {
				if added.declaration.columnNotNull {
					next, err = notNullCandidate(op, auth.catalog, next, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: added.column.Name}, Constraint: parser.Identifier{Text: added.declaration.columnName}})
					if err != nil {
						break
					}
				}
			}
		}
		if err == nil && pass == 4 {
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
			err = e.bindColumnChecks(op, auth, raw, next, scope.input, true)
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
	changes = cloneCatalogChanges(changes)
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
	rewrite := false
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
	}
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
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
	rewrite = rewrite || volatileAdd
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
	projectedWrites := scope.ownWrites[ref.Address]
	projectedDeletes := scope.ownDeletes[ref.Address]
	validator := alterRowValidator{relation: next, schema: schema}
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
		replacement, projectedWrites, projectedDeletes, err = rewriteTypeRows(scope, op, old, next, store, frontier, transformations, additions, validator)
		if err != nil {
			return Result{}, err
		}
		versions, ends = replacement.versions, replacement.ends
		shape.rewritten, shape.frontier = true, frontier
		shape.snapshot, shape.hasSnapshot = nil, false
	} else if !slices.Equal(old.attributes, next.attributes) {
		if store.generation == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: storage generation exhausted")
		}
		projection, projectionErr := newColumnProjection(old, next, shape.fills)
		if projectionErr != nil {
			return Result{}, projectionErr
		}
		versions, err = projection.versions(op.Context(), versions)
		if err != nil {
			return Result{}, err
		}
		replacement = &relationStorage{versions: versions, ends: ends, generation: store.generation + 1}
		projectedWrites, err = projection.versions(op.Context(), projectedWrites)
		if err != nil {
			return Result{}, err
		}
		projectedDeletes = projectDeletions(projectedDeletes, replacement)
		if scope.owner.isolation == RepeatableRead && snapshotExists && !shape.rewritten {
			if slices.Equal(snapshot.relation.attributes, old.attributes) {
				shape.snapshot, err = projection.versions(op.Context(), snapshot.versions)
			} else {
				shape.snapshot, err = projectVersions(op.Context(), snapshot.versions, snapshot.relation, next, shape.fills)
			}
			if err != nil {
				return Result{}, err
			}
			shape.hasSnapshot = true
		}
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
	// Record all originals before provider callbacks; Reset cannot restamp them.
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	if err := e.evolveStageIndexesLocked(stage); err != nil {
		e.mu.Unlock()
		return Result{}, err
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
	shape.replacement, shape.relation = replacement, next
	changes.storage[ref.Address] = shape
	e.mu.Unlock()
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	// A shape statement replaces prior own roots only on the successful merge.
	if replacement != store {
		scope.projectedRoots = true
		if len(scope.ownWrites) != 0 {
			scope.stage.rows = make(map[ObjectAddress][]rowVersion, len(scope.ownWrites))
			for a, rows := range scope.ownWrites {
				scope.stage.rows[a] = rows
			}
			scope.stage.rows[ref.Address] = projectedWrites
			scope.stage.replacing = true
		}
		scope.stage.deletes = make(map[ObjectAddress]*privateDeletionRoot, len(scope.ownDeletes)+1)
		for a, root := range scope.ownDeletes {
			scope.stage.deletes[a] = root
		}
		scope.stage.deletes[ref.Address] = projectedDeletes
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
		if _, ok := old.identities[old.attributes[slot]]; ok {
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
		for _, c := range old.notNullConstraints {
			if c.Name == name {
				return nil, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.name))
			}
		}
		for _, c := range old.checkConstraints {
			if c.Name == name {
				return nil, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.name))
			}
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
