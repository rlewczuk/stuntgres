package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
	next := old
	var transformations []columnTransformation
	var pendingChecks []alterTypeCheck
	var typeChecks []checkConstraint
	// PostgreSQL runs DROP, TYPE, original SET, ADD NN, then identity ADD/SET.
	for pass := range 6 {
		for _, action := range s.Actions {
			selected := pass == 0 && (action.Action == parser.DropIdentity || action.Action == parser.DropNotNull || action.Action == parser.DropConstraint || action.Action == parser.DropColumn) || pass == 1 && action.Action == parser.AlterColumnType || pass == 2 && action.Action == parser.SetNotNull || pass == 3 && action.Action == parser.AddNotNull || pass == 4 && action.Action == parser.AddIdentity || pass == 5 && action.Action == parser.SetIdentity
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
			} else if action.Action == parser.AlterColumnType {
				var transformation columnTransformation
				next, transformation, err = e.alterTypeCandidate(scope, op, auth, stage, old, next, ref, action)
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
	rewrite := false
	for _, transformation := range transformations {
		rewrite = rewrite || transformation.rewrite
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
		e.captureCheckCommentSnapshotLocked(scope, &traversal)
		e.mu.Unlock()
	}
	if rewrite {
		replacement, projectedWrites, projectedDeletes, err = rewriteTypeRows(scope, op, old, next, store, frontier, transformations, validator)
		if err != nil {
			return Result{}, err
		}
		versions, ends = replacement.versions, replacement.ends
		shape.rewritten, shape.frontier = true, frontier
		shape.snapshot, shape.hasSnapshot = nil, false
	} else if len(old.columns) != len(next.columns) {
		if store.generation == ^uint64(0) {
			return Result{}, fmt.Errorf("stuntgres: storage generation exhausted")
		}
		versions, err = projectVersions(op.Context(), versions, old, next)
		if err != nil {
			return Result{}, err
		}
		replacement = &relationStorage{versions: versions, ends: ends, generation: store.generation + 1}
		projectedWrites, err = projectVersions(op.Context(), projectedWrites, old, next)
		if err != nil {
			return Result{}, err
		}
		projectedDeletes = projectDeletions(projectedDeletes, replacement)
		if scope.owner.isolation == RepeatableRead && snapshotExists && !shape.rewritten {
			shape.snapshot, err = projectVersions(op.Context(), snapshot.versions, snapshot.relation, next)
			if err != nil {
				return Result{}, err
			}
			shape.hasSnapshot = true
		}
	}
	needsScan := !rewrite && len(typeChecks) != 0
	if !rewrite {
		for _, column := range next.columns {
			if !column.Nullable && old.columns[old.index[column.Name]].Nullable {
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
	changes = cloneCatalogChanges(changes)
	if stage == nil {
		stage = e.BeginCatalogStage(auth.catalog)
	}
	if err := stage.Replace(next); err != nil {
		return Result{}, err
	}
	if len(typeChecks) != 0 {
		if err := scope.acquireCheckProviders(stage); err != nil {
			return Result{}, err
		}
	}
	var dependencyTargets []ObjectAddress
	for i, attribute := range old.attributes {
		if _, survives := next.index[old.columns[i].Name]; !survives {
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
		changes.generators[a] = generator
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
		for address := range stage.generators {
			if memory := op.session.sequences[address]; memory != nil {
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
