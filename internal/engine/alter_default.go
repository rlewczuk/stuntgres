package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The owner-authorized AX grant protects identity; publication changes only the
// immutable descriptor and never evaluates a default or rewrites row storage.
// Every return releases Engine.mu.
func (e *Engine) alterDefaultLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableDefault, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if s.Action != parser.SetDefault && s.Action != parser.DropDefault || s.Action == parser.SetDefault && s.Expression == nil || s.Action == parser.DropDefault && s.Expression != nil {
		return fail(fmt.Errorf("stuntgres: invalid default alteration"))
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
	if hierarchyRelation(old) && !partitionDefaultReplayAllowed(e, op, auth, s) {
		return fail(sqlError("0A000", "altering partition hierarchies is unsupported"))
	}
	if err := tableKind(old, "ALTER COLUMN ... SET DEFAULT"); err != nil {
		return fail(err)
	}
	if err := inheritanceAlterGuard(auth.catalog, old); err != nil {
		return fail(err)
	}
	if isSystemColumn(s.Column.Text) {
		return fail(sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, s.Column.Text)))
	}
	slot, exists := old.index[s.Column.Text]
	if !exists {
		return fail(sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, s.Column.Text, old.name)))
	}
	attr := old.attributes[slot]
	if _, generated := old.generations[attr]; generated {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is a generated column`, s.Column.Text, old.name)).(*sqlstate.Error)
		diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... SET EXPRESSION instead."
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP EXPRESSION instead."
		}
		return fail(diagnostic)
	}
	if _, ok := old.identities[attr]; ok {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, s.Column.Text, old.name)).(*sqlstate.Error)
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP IDENTITY instead."
		}
		return fail(diagnostic)
	}
	var definition columnDefault
	if s.Action == parser.SetDefault {
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.queryAuth = &auth
		binder.defaultTemporary = auth.catalog.state.descriptors[old.namespace].(*namespaceDescriptor).temporary
		definition.expression, err = binder.bindDefault(s.Expression, old.columns[slot])
		if err == nil {
			_, err = (scalarRenderer{dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot, regtypeReferences: &definition.regtypeReferences}).sql(op.Context(), auth.catalog, definition.expression)
		}
		if err != nil {
			// PostgreSQL cooks ALTER defaults without source text. CREATE diagnostics
			// and parser syntax diagnostics retain their own source positions.
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				copy := *diagnostic
				copy.Position = ""
				err = &copy
			}
			return fail(err)
		}
		if structuralNull(definition.expression) {
			definition = columnDefault{}
		} else {
			definition.OID, err = e.allocateOIDLocked()
			if err != nil {
				return fail(err)
			}
		}
	} else if _, present := old.defaults[attr]; !present {
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
		return Result{}, err
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	original := e.authorizationStorageLocked(auth, ref.Address)
	if original == nil && old.partition == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	var length int
	var generation uint64
	var ends *deletionRoot
	if original != nil {
		length, generation, ends = len(original.versions), original.generation, original.ends
	}
	next := *old
	next.revision++
	capacity := len(old.defaults)
	if definition.expression == nil {
		if _, present := old.defaults[attr]; present {
			capacity--
		}
	} else if _, present := old.defaults[attr]; !present {
		capacity++
	}
	next.defaults = nil
	if capacity != 0 {
		next.defaults = make(map[int32]columnDefault, capacity)
	}
	for key, value := range old.defaults {
		if key != attr {
			next.defaults[key] = value
		}
	}
	if definition.expression != nil {
		next.defaults[attr] = definition
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if original == nil {
		err = stage.Replace(&next)
	} else {
		err = stage.alterRelation(op.Context(), ref, &next, original, original, length, generation, ends)
	}
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
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
