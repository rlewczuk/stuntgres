package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The caller holds Engine.mu and an owner-authorized AX grant. Every path
// releases Engine.mu; descriptor publication never rewrites row storage.
func (e *Engine) alterNotNullLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableNotNull, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if s.Action < parser.SetNotNull || s.Action > parser.DropConstraint || s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		return fail(fmt.Errorf("stuntgres: invalid not-null alteration"))
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
	action := "ALTER COLUMN ... SET NOT NULL"
	switch s.Action {
	case parser.DropNotNull:
		action = "ALTER COLUMN ... DROP NOT NULL"
	case parser.AddNotNull:
		action = "ADD CONSTRAINT"
	case parser.DropConstraint:
		action = "DROP CONSTRAINT"
	}
	if err := tableKind(old, action); err != nil {
		return fail(err)
	}
	slot := -1
	constraintIndex := -1
	if s.Action == parser.DropConstraint {
		for i, c := range old.notNullConstraints {
			if c.Name == s.Constraint.Text {
				constraintIndex = i
				for j, attr := range old.attributes {
					if attr == c.Attribute {
						slot = j
						break
					}
				}
				break
			}
		}
		if slot < 0 && !s.IfExists {
			return fail(sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, s.Constraint.Text, old.name)))
		}
	} else {
		if isSystemColumn(s.Column.Text) {
			if s.Action == parser.AddNotNull {
				return fail(sqlError("0A000", fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, s.Column.Text)))
			}
			return fail(sqlError("0A000", "system-column nullability is unsupported"))
		}
		var exists bool
		slot, exists = old.index[s.Column.Text]
		if !exists {
			return fail(sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, s.Column.Text, old.name)))
		}
		for i, c := range old.notNullConstraints {
			if c.Attribute == old.attributes[slot] {
				constraintIndex = i
				break
			}
		}
	}
	adding := s.Action == parser.SetNotNull || s.Action == parser.AddNotNull
	if slot < 0 || adding && constraintIndex >= 0 || !adding && constraintIndex < 0 {
		op.session.mu.Lock()
		err = publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err == nil {
			scope.completed = true
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		if slot < 0 {
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist, skipping`, s.Constraint.Text, old.name)})
		}
		return Result{}, nil
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	original := e.storage[ref.Address]
	if original == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	versions := original.versions
	observedLength := len(versions)
	observedGeneration := original.generation
	frontier := e.commitSeq
	ends := original.ends
	name := s.Constraint.Text
	if adding {
		if name == "" {
			name = generatedNotNullName(old.name, old.columns[slot].Name, constraintNames(auth.catalog, old.namespace))
		}
		for _, c := range old.notNullConstraints {
			if c.Name == name {
				return fail(sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.name)))
			}
		}
	}
	schema := auth.catalog.state.descriptors[old.namespace].Name()
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if adding {
		for _, row := range versions {
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if liveVersion(row, frontier, ends, nil) && row.born != 0 && row.values[slot].IsNull() {
				diagnostic := sqlError("23502", fmt.Sprintf(`column "%s" of relation "%s" contains null values`, old.columns[slot].Name, old.name)).(*sqlstate.Error)
				diagnostic.Schema, diagnostic.Table, diagnostic.Column = schema, old.name, old.columns[slot].Name
				return Result{}, diagnostic
			}
		}
	}
	next := *old
	next.revision++
	next.columns = append([]ColumnDescriptor(nil), old.columns...)
	next.columns[slot].Nullable = !adding
	if adding {
		next.notNullConstraints = make([]notNullConstraint, len(old.notNullConstraints)+1)
		copy(next.notNullConstraints, old.notNullConstraints)
		next.notNullConstraints[len(old.notNullConstraints)] = notNullConstraint{Name: name, Attribute: old.attributes[slot]}
	} else {
		next.notNullConstraints = make([]notNullConstraint, 0, len(old.notNullConstraints)-1)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[:constraintIndex]...)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[constraintIndex+1:]...)
	}
	if err := stage.alterRelation(op.Context(), ref, &next, original, original, observedLength, observedGeneration, ends); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
