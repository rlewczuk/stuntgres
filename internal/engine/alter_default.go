package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The owner-authorized AX grant protects identity; publication changes only the
// immutable descriptor and never evaluates a default or rewrites row storage.
// Every return releases Engine.mu.
func (e *Engine) alterDefaultLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTableDefault, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if s.Action != parser.SetDefault && s.Action != parser.DropDefault || s.Action == parser.SetDefault && s.Expression == nil || s.Action == parser.DropDefault && s.Expression != nil {
		return fail(fmt.Errorf("stuntgres: invalid default alteration"))
	}
	ref, err := e.resolveReference(auth, s.Name)
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
	if catalog.HierarchyRelation(old) && !partitionDefaultReplayAllowed(e, op, auth, s) {
		return fail(sqlError("0A000", "altering partition hierarchies is unsupported"))
	}
	if err := analyze.TableKindError(old, "ALTER COLUMN ... SET DEFAULT"); err != nil {
		return fail(err)
	}
	if catalog.IsSystemColumn(s.Column.Text) {
		return fail(sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, s.Column.Text)))
	}
	slot, exists := old.Index[s.Column.Text]
	if !exists {
		return fail(sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, s.Column.Text, old.Name())))
	}
	attr := old.Attributes[slot]
	if _, generated := old.ColumnMeta.Generations[attr]; generated {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is a generated column`, s.Column.Text, old.Name())).(*sqlstate.Error)
		diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... SET EXPRESSION instead."
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP EXPRESSION instead."
		}
		return fail(diagnostic)
	}
	if _, ok := old.ColumnMeta.Identities[attr]; ok {
		diagnostic := sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, s.Column.Text, old.Name())).(*sqlstate.Error)
		if s.Action == parser.DropDefault {
			diagnostic.Hint = "Use ALTER TABLE ... ALTER COLUMN ... DROP IDENTITY instead."
		}
		return fail(diagnostic)
	}
	if catalog.InheritanceParticipant(auth.Catalog, old) {
		return e.alterInheritanceDefaultLocked(scope, op, auth, s, old)
	}
	var definition catalog.ColumnDefault
	if s.Action == parser.SetDefault {
		definition, err = e.cookAlterDefaultLocked(scope, op, auth, old, slot, s.Expression)
		if err != nil {
			return fail(err)
		}
	} else if _, present := old.ColumnMeta.Defaults[attr]; !present {
		op.Session.Mu.Lock()
		err = publicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err == nil {
			scope.completed = true
		}
		e.mu.Unlock()
		return Result{}, err
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	original := e.authorizationStorageLocked(auth, ref.Address)
	if original == nil && old.Partition == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	var length int
	var generation uint64
	var ends *storage.DeletionRoot
	if original != nil {
		length, generation, ends = len(original.Versions), original.Generation, original.Ends
	}
	next := *old
	next.ObjectRevision++
	capacity := len(old.ColumnMeta.Defaults)
	if definition.Expression == nil {
		if _, present := old.ColumnMeta.Defaults[attr]; present {
			capacity--
		}
	} else if _, present := old.ColumnMeta.Defaults[attr]; !present {
		capacity++
	}
	next.ColumnMeta.Defaults = nil
	if capacity != 0 {
		next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, capacity)
	}
	for key, value := range old.ColumnMeta.Defaults {
		if key != attr {
			next.ColumnMeta.Defaults[key] = value
		}
	}
	if definition.Expression != nil {
		next.ColumnMeta.Defaults[attr] = definition
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
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
	if _, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}

func (e *Engine) cookAlterDefaultLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, slot int, expression parser.Expr) (catalog.ColumnDefault, error) {
	var definition catalog.ColumnDefault
	binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
	binder.Query.Auth = &auth
	binder.Consumer.DefaultTemporary = auth.Catalog.MustNamespace(old.Namespace()).Temporary
	var err error
	definition.Expression, err = binder.BindDefault(expression, old.Columns[slot])
	if err == nil {
		_, err = (scalarRenderer{aclOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot, catalogAliasReferences: &definition.CatalogAliasReferences}).sql(op.Context(), auth.Catalog, definition.Expression)
	}
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return catalog.ColumnDefault{}, err
	}
	if omitColumnDefault(auth.Catalog, definition.Expression, old.Columns[slot].Usage) {
		return catalog.ColumnDefault{}, nil
	}
	definition.OID, err = e.catalog.allocateOIDLocked()
	return definition, err
}
