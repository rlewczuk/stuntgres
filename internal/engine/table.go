package engine

import (
	"fmt"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called with e.mu held; every return releases it. Preflight the whole group
// before staging removals. There are no supported inbound table-definition
// dependencies, so RESTRICT and CASCADE have the same closure.
func (e *Engine) dropTableLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.DropTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if len(s.Names) == 0 || s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: invalid table drop")
	}
	refs := make([]ObjectRef, 0, len(s.Names))
	seen := make(map[ObjectAddress]bool, len(s.Names))
	var notices []sqlstate.Error
	for _, name := range s.Names {
		if len(name.Parts) < 1 || len(name.Parts) > 2 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid relation name")
		}
		kind, missing := "table", name.Parts[len(name.Parts)-1].Text
		if len(name.Parts) == 2 {
			_, exists := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
			if !exists {
				kind, missing = "schema", name.Parts[0].Text
			}
		}
		var ref ObjectRef
		var err error
		if kind == "schema" {
			err = sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, missing))
		} else {
			ref, err = e.resolveReference(auth, name)
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42P01" {
				err = sqlError("42P01", fmt.Sprintf(`table "%s" does not exist`, missing))
			}
		}
		if err != nil {
			d, ok := err.(*sqlstate.Error)
			if !s.IfExists || !ok || (d.Code != "42P01" && d.Code != "3F000") {
				e.mu.Unlock()
				return Result{}, err
			}
			notices = append(notices, sqlstate.Error{
				Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000",
				Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, missing),
			})
			continue
		}
		if seen[ref.Address] {
			continue
		}
		tableErr := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
		if tableErr != nil {
			descriptor := auth.catalog.state.descriptors[ref.Address]
			nsref := ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: descriptor.Namespace()}
			if err := auth.Check(AccessRequirement{Object: nsref, OwnerOnly: true}); err != nil {
				e.mu.Unlock()
				return Result{}, tableErr
			}
		}
		seen[ref.Address] = true
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		e.mu.Unlock()
		for _, notice := range notices {
			op.emitNotice(levelNotice, notice)
		}
		return Result{}, nil
	}
	base := auth.catalog
	stage := e.BeginCatalogStage(base)
	stage.authSession = op.session
	stage.authRevision = auth.revision
	stage.roleRevision = auth.roles.state.revision
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, ref := range refs {
		if err := stage.Remove(ref); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	for _, notice := range notices {
		op.emitNotice(levelNotice, notice)
	}
	return Result{}, nil
}

func isSystemColumn(name string) bool {
	switch name {
	case "ctid", "xmin", "cmin", "xmax", "cmax", "tableoid":
		return true
	}
	return false
}

// Called with e.mu held. The supported dependent set is column nullability,
// ACLs and comments; no native external definition providers exist.
func (e *Engine) alterDropColumnLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableDropColumn, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Mode != parser.DropRestrict && s.Mode != parser.DropCascade {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: invalid column drop")
	}
	ref, err := e.resolveReference(auth, s.Name)
	// Resolution may change while AX waits (for example, a new search_path
	// shadow). Never operate on a relation other than the acquired resource.
	if err == nil && ref != scope.lockedRef {
		err = ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	old := scope.lockedDescriptor.(*relationDescriptor)
	if isSystemColumn(s.Column.Text) {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", fmt.Sprintf(`cannot drop system column "%s"`, s.Column.Text))
	}
	slot, exists := old.index[s.Column.Text]
	if !exists {
		if !s.IfExists {
			e.mu.Unlock()
			return Result{}, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, s.Column.Text, old.name))
		}
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
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" does not exist, skipping`, s.Column.Text, old.name)})
		return Result{}, nil
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	original := e.storage[ref.Address]
	if original == nil {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: relation storage is missing")
	}
	originalVersions := original.versions
	originalEnds := original.ends
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next := *old
	next.revision++
	next.columns = make([]ColumnDescriptor, 0, len(old.columns)-1)
	next.attributes = make([]int32, 0, len(old.attributes)-1)
	next.index = make(map[string]int, len(old.index)-1)
	for i, column := range old.columns {
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		if i == slot {
			continue
		}
		next.index[column.Name] = len(next.columns)
		next.columns = append(next.columns, column)
		next.attributes = append(next.attributes, old.attributes[i])
	}
	next.notNullConstraints = nil
	for _, constraint := range old.notNullConstraints {
		if constraint.Attribute != old.attributes[slot] {
			next.notNullConstraints = append(next.notNullConstraints, constraint)
		}
	}
	if _, exists := old.columnACL[old.attributes[slot]]; exists {
		next.columnACL = nil
		for sub, acl := range old.columnACL {
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnACL == nil {
				next.columnACL = make(map[int32]ACLValue, len(old.columnACL)-1)
			}
			next.columnACL[sub] = acl
		}
	}
	if _, exists := old.columnComments[old.attributes[slot]]; exists {
		next.columnComments = nil
		for sub, text := range old.columnComments {
			if err := op.Context().Err(); err != nil {
				return Result{}, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnComments == nil {
				next.columnComments = make(map[int32]string, len(old.columnComments)-1)
			}
			next.columnComments[sub] = text
		}
	}
	replacement := &relationStorage{versions: make([]rowVersion, len(originalVersions)), ends: originalEnds}
	for i, row := range originalVersions {
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		if len(row.values) != len(old.columns) {
			return Result{}, fmt.Errorf("stuntgres: invalid original row width")
		}
		values := make([]sqltype.Value, len(next.columns))
		copy(values, row.values[:slot])
		copy(values[slot:], row.values[slot+1:])
		row.values = values
		replacement.versions[i] = row
	}
	if err := stage.alterRelation(op.Context(), ref, &next, original, replacement, len(originalVersions), originalEnds); err != nil {
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
