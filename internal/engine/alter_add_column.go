package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Added defaults are execution-private. Only captured nonvolatile values enter
// transaction shape projections; persistent definitions retain their bound tree.
type addedColumn struct {
	attribute    int32
	column       *parser.Column
	declaration  analyze.ColumnDeclaration
	expression   expr.Expr
	dependencies []ids.ObjectRef // Borrowed cooked DEFAULT manifest, distinct from the ALTER statement.
	consumer     *catalog.Relation
	value        sqltype.Value
}

func alterCookingError(err error) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position = ""
		return &copy
	}
	return err
}

func (e *Engine) addColumnCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, addedColumn, error) {
	scope.routineAuth = auth
	fail := func(err error) (*catalog.Relation, addedColumn, error) { return nil, addedColumn{}, err }
	column := action.Definition
	if column == nil {
		return fail(fmt.Errorf("stuntgres: missing ADD column declaration"))
	}
	for _, constraint := range column.Constraints {
		if constraint.Unique != nil && constraint.Unique.Primary {
			return fail(insertError("0A000", "ALTER TABLE action is unsupported", action.Pos))
		}
	}
	if old.RelationKind != catalog.CompositeRelation && catalog.IsSystemColumn(column.Name) {
		return fail(sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name)))
	}
	if _, exists := old.Index[column.Name]; exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" already exists`, column.Name, old.Name())
		if !action.IfNotExists {
			return fail(sqlError("42701", message))
		}
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: message + ", skipping"})
		return old, addedColumn{}, nil
	}
	if old.ColumnMeta.HighWater >= 1600 {
		return fail(sqlError("54011", "tables can have at most 1600 columns"))
	}
	if sequence.SerialTypeName(column.TypeName) != "" {
		return fail(insertError("0A000", "ADD COLUMN serial declarations are unsupported", column.Pos))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if auth.Catalog.Epoch() != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	var usage sqltype.Usage
	var err error
	if captured != nil {
		usage = *captured
	} else {
		usage, err = e.resolveType(column.TypeName, op)
		if err == nil {
			usage, err = e.resolveColumnCollation(usage, column.Collation, op)
		}
		if err != nil {
			return fail(err)
		}
	}
	declaration, err := analyze.BindCreateColumnDeclaration(column, old.Name())
	if err != nil {
		return fail(err)
	}
	if declaration.Identity != nil {
		return fail(insertError("0A000", "ADD COLUMN identity declarations are unsupported", declaration.Identity.Pos))
	}
	if declaration.Generation != nil {
		return fail(insertError("0A000", "ADD COLUMN generated declarations are unsupported", declaration.Generation.Pos))
	}
	attribute := old.ColumnMeta.HighWater + 1
	next := *old
	next.ColumnMeta.HighWater = attribute
	next.Columns = slices.Clone(old.Columns)
	next.Attributes = slices.Clone(old.Attributes)
	next.Index = maps.Clone(old.Index)
	next.ColumnMeta.Defaults = maps.Clone(old.ColumnMeta.Defaults)
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	next.Constraints.Check = slices.Clone(old.Constraints.Check)
	next.Index[column.Name] = len(next.Columns)
	definition := catalog.Column{Name: column.Name, Usage: usage, DeclaredDimensions: int16(len(column.TypeName.ArrayBounds)), Nullable: true, NullabilityKnown: true, OriginTable: uint32(old.Address().ObjectID), OriginAttribute: int16(attribute)}
	next.Columns = append(next.Columns, definition)
	next.Attributes = append(next.Attributes, attribute)
	added := addedColumn{attribute: attribute, column: column, declaration: declaration, value: sqltype.Null(usage.Type)}
	node, origin := analyze.ColumnDefault(auth.Catalog, &next, len(next.Columns)-1)
	added.expression = node
	added.dependencies, added.consumer = origin.Definition.Dependencies, origin.Consumer
	if sqltype.ContainsDomainConstraints(usage.Type) && scope.input.TypeDiagnosticName == nil {
		var err error
		scope.input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		if err != nil {
			return fail(err)
		}
	}
	if declaration.DefaultExpr != nil {
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.Query.Auth = &auth
		binder.Consumer.DefaultTemporary = auth.Catalog.MustNamespace(old.Namespace()).Temporary
		node, err := binder.BindDefault(declaration.DefaultExpr, definition)
		if err != nil {
			return fail(alterCookingError(err))
		}
		if !omitColumnDefault(auth.Catalog, node, usage) {
			persisted := catalog.ColumnDefault{Expression: node}
			if _, err := (scalarRenderer{definition: op.Definition, aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, dependencies: &persisted.Dependencies, commentSnapshot: &persisted.CatalogSnapshot, catalogAliasReferences: &persisted.CatalogAliasReferences}).sql(op.Context(), auth.Catalog, node); err != nil {
				return fail(alterCookingError(err))
			}
			if next.ColumnMeta.Defaults == nil {
				next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
			}
			next.ColumnMeta.Defaults[attribute] = persisted
			added.expression = node
			added.dependencies, added.consumer = persisted.Dependencies, old
		}
	}
	if err := refreshGenerationChecks(&next, auth.Catalog); err != nil {
		return fail(err)
	}
	return &next, added, nil
}

func planAddedColumns(scope *StatementScope, additions []addedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	perRow := false
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: scope.routineAuth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for i := range additions {
		added := &additions[i]
		if added.expression != nil {
			recipe := evaluation
			recipe.RoutineRecipe, recipe.RoutineGraft = true, false
			recipe.RoutineReferences, recipe.RoutineConsumer = added.dependencies, added.consumer
			planned, _, err := preflightCoercionsWithPolicy(&recipe, added.expression, true)
			if err != nil {
				return nil, false, alterCookingError(err)
			}
			if err := validateExpressionJoinExecution(&recipe, planned, recipe.Catalog); err != nil {
				return nil, false, alterCookingError(err)
			}
			added.expression = planned
			properties := planned.Properties()
			if properties.Volatility > expr.Stable || properties.RuntimeDomainConversion {
				perRow = true
				continue
			}
			added.value, err = planned.Eval(&evaluation)
			if err != nil {
				return nil, false, alterCookingError(err)
			}
		}
		if fills == nil {
			fills = make(map[int32]sqltype.Value, len(additions))
		}
		fills[added.attribute] = added.value
	}
	return fills, perRow, nil
}
