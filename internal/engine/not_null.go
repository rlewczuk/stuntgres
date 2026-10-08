package engine

import (
	"fmt"
	"slices"
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
	// PostgreSQL runs DROP passes before original SET, then transformed ADD NN.
	for pass := range 3 {
		for _, action := range s.Actions {
			selected := pass == 0 && (action.Action == parser.DropNotNull || action.Action == parser.DropConstraint || action.Action == parser.DropColumn) || pass == 1 && action.Action == parser.SetNotNull || pass == 2 && action.Action == parser.AddNotNull
			if !selected {
				continue
			}
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if action.Action == parser.DropColumn {
				if _, exists := next.index[action.Column.Text]; exists && stage == nil {
					stage = e.BeginCatalogStage(auth.catalog)
				}
				next, err = scope.dropColumnCandidate(stage, next, ref, action)
			} else {
				next, err = notNullCandidate(op, auth.catalog, next, action)
			}
			if err != nil {
				return Result{}, err
			}
		}
	}
	if next == old || len(next.columns) == len(old.columns) && slices.Equal(next.notNullConstraints, old.notNullConstraints) {
		return Result{}, nil
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.revision = old.revision + 1
	replacement := store
	projectedWrites := scope.ownWrites[ref.Address]
	projectedDeletes := scope.ownDeletes[ref.Address]
	if len(old.columns) != len(next.columns) {
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
		if scope.owner.isolation == RepeatableRead && snapshotExists {
			shape.snapshot, err = projectVersions(op.Context(), snapshot.versions, snapshot.relation, next)
			if err != nil {
				return Result{}, err
			}
			shape.hasSnapshot = true
		}
	}
	needsScan := false
	for _, column := range next.columns {
		if !column.Nullable && old.columns[old.index[column.Name]].Nullable {
			needsScan = true
			break
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
	scan := func(rows []rowVersion, committed bool) error {
		for _, row := range rows {
			if err := op.Context().Err(); err != nil {
				return err
			}
			if committed && replaced[row.row] || !liveVersion(row, frontier, ends, projectedDeletes) {
				continue
			}
			for slot, column := range next.columns {
				if !column.Nullable && row.values[slot].IsNull() {
					d := sqlError("23502", fmt.Sprintf(`column "%s" of relation "%s" contains null values`, column.Name, next.name)).(*sqlstate.Error)
					d.Schema, d.Table, d.Column = schema, next.name, column.Name
					return d
				}
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
	var dependencyTargets []ObjectAddress
	for i, attribute := range old.attributes {
		if _, survives := next.index[old.columns[i].Name]; !survives {
			target := ref.Address
			target.SubID = attribute
			dependencyTargets = append(dependencyTargets, target)
		}
	}
	for a := range stage.removals {
		dependencyTargets = append(dependencyTargets, a)
	}
	if len(dependencyTargets) != 0 {
		changes.dependencies = append(changes.dependencies, catalogDependencyObservation{grantCatalog, dependencyTargets})
	}
	// Record all originals before provider callbacks; Reset cannot restamp them.
	e.mu.Lock()
	if ref.Epoch != e.state.epoch {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	for a, d := range stage.descriptors {
		if e.state.descriptors[a] != grantCatalog.descriptors[a] {
			e.mu.Unlock()
			return Result{}, CatalogConflict{}
		}
		e.catalogChangeLocked(changes, a, d)
	}
	for a := range stage.removals {
		if e.state.descriptors[a] != grantCatalog.descriptors[a] {
			e.mu.Unlock()
			return Result{}, CatalogConflict{}
		}
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.generators, a)
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
			if len(projectedWrites) != 0 {
				scope.stage.rows[ref.Address] = projectedWrites
			}
			scope.stage.replacing = true
		}
		if len(scope.ownDeletes) != 0 {
			scope.stage.deletes = make(map[ObjectAddress]*privateDeletionRoot, len(scope.ownDeletes))
		}
		for a, root := range scope.ownDeletes {
			scope.stage.deletes[a] = root
		}
		if projectedDeletes != nil {
			scope.stage.deletes[ref.Address] = projectedDeletes
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
	if adding && index >= 0 || !adding && index < 0 {
		return old, nil
	}
	name := a.Constraint.Text
	if adding {
		if name == "" {
			name = generatedNotNullName(old.name, old.columns[slot].Name, constraintNames(catalog, old.namespace, old))
		}
		for _, c := range old.notNullConstraints {
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
