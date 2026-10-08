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
	if err := tableKind(old, "ALTER COLUMN ... SET DEFAULT"); err != nil {
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
		definition.expression, err = binder.bindDefault(s.Expression, old.columns[slot])
		if err == nil {
			definition.sql, err = defaultSQL(op.Context(), auth.catalog, definition.expression)
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
	original := e.storage[ref.Address]
	if original == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	length, generation, ends := len(original.versions), original.generation, original.ends
	next := *old
	next.revision++
	capacity := len(old.defaults)
	if s.Action == parser.DropDefault {
		capacity--
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
	if s.Action == parser.SetDefault {
		next.defaults[attr] = definition
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err := stage.alterRelation(op.Context(), ref, &next, original, original, length, generation, ends); err != nil {
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
