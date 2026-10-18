package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Added defaults are execution-private. Only captured nonvolatile values enter
// transaction shape projections; persistent definitions retain their bound tree.
type addedColumn struct {
	attribute    int32
	column       *parser.Column
	declaration  createColumnDeclaration
	expression   expr.Expr
	dependencies []ids.ObjectRef // Borrowed cooked DEFAULT manifest, distinct from the ALTER statement.
	consumer     *relationDescriptor
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

func (e *Engine) addColumnCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, old *relationDescriptor, action parser.AlterTableAction, captured *sqltype.Usage) (*relationDescriptor, addedColumn, error) {
	scope.routineAuth = auth
	fail := func(err error) (*relationDescriptor, addedColumn, error) { return nil, addedColumn{}, err }
	column := action.Definition
	if column == nil {
		return fail(fmt.Errorf("stuntgres: missing ADD column declaration"))
	}
	for _, constraint := range column.Constraints {
		if constraint.Unique != nil && constraint.Unique.Primary {
			return fail(insertError("0A000", "ALTER TABLE action is unsupported", action.Pos))
		}
	}
	if old.RelationKind != compositeRelation && isSystemColumn(column.Name) {
		return fail(sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name)))
	}
	if _, exists := old.Index[column.Name]; exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" already exists`, column.Name, old.name)
		if !action.IfNotExists {
			return fail(sqlError("42701", message))
		}
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: message + ", skipping"})
		return old, addedColumn{}, nil
	}
	if old.AttributeHighWater >= 1600 {
		return fail(sqlError("54011", "tables can have at most 1600 columns"))
	}
	if serialTypeName(column.TypeName) != "" {
		return fail(insertError("0A000", "ADD COLUMN serial declarations are unsupported", column.Pos))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if auth.catalog.Epoch() != e.catalog.state.epoch {
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
	declaration, err := bindCreateColumnDeclaration(column, old.name)
	if err != nil {
		return fail(err)
	}
	if declaration.identity != nil {
		return fail(insertError("0A000", "ADD COLUMN identity declarations are unsupported", declaration.identity.Pos))
	}
	if declaration.generation != nil {
		return fail(insertError("0A000", "ADD COLUMN generated declarations are unsupported", declaration.generation.Pos))
	}
	attribute := old.AttributeHighWater + 1
	next := *old
	next.AttributeHighWater = attribute
	next.Columns = slices.Clone(old.Columns)
	next.Attributes = slices.Clone(old.Attributes)
	next.Index = maps.Clone(old.Index)
	next.Defaults = maps.Clone(old.Defaults)
	next.NotNullConstraints = slices.Clone(old.NotNullConstraints)
	next.CheckConstraints = slices.Clone(old.CheckConstraints)
	next.Index[column.Name] = len(next.Columns)
	definition := ColumnDescriptor{Name: column.Name, Usage: usage, DeclaredDimensions: int16(len(column.TypeName.ArrayBounds)), Nullable: true, NullabilityKnown: true, OriginTable: uint32(old.address.ObjectID), OriginAttribute: int16(attribute)}
	next.Columns = append(next.Columns, definition)
	next.Attributes = append(next.Attributes, attribute)
	added := addedColumn{attribute: attribute, column: column, declaration: declaration, value: sqltype.Null(usage.Type)}
	if usage.Type.HasDomainChecks() {
		// The omitted value is fresh base-type NULL, not an already checked
		// domain value. Keep this input conversion execution-private.
		added.expression = domainNullInputExpression(usage)
	}
	if sqltype.ContainsDomainChecks(usage.Type) && scope.input.TypeDiagnosticName == nil {
		var err error
		scope.input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		if err != nil {
			return fail(err)
		}
	}
	if declaration.defaultExpr != nil {
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.queryAuth = &auth
		binder.defaultTemporary = auth.catalog.MustNamespace(old.namespace).temporary
		node, err := binder.bindDefault(declaration.defaultExpr, definition)
		if err != nil {
			return fail(alterCookingError(err))
		}
		if !structuralNull(node) {
			persisted := columnDefault{expression: node}
			if _, err := (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, dependencies: &persisted.dependencies, commentSnapshot: &persisted.catalogSnapshot, catalogAliasReferences: &persisted.catalogAliasReferences}).sql(op.Context(), auth.catalog, node); err != nil {
				return fail(alterCookingError(err))
			}
			if next.Defaults == nil {
				next.Defaults = make(map[int32]columnDefault)
			}
			next.Defaults[attribute] = persisted
			added.expression = node
			added.dependencies, added.consumer = persisted.dependencies, old
		}
	}
	if err := refreshGenerationChecks(&next, auth.catalog); err != nil {
		return fail(err)
	}
	return &next, added, nil
}

func planAddedColumns(scope *StatementScope, additions []addedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	perRow := false
	evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: scope.routineAuth.catalog}
	for i := range additions {
		added := &additions[i]
		if added.expression != nil {
			recipe := evaluation
			recipe.routineRecipe, recipe.routineGraft = true, false
			recipe.routineReferences, recipe.routineConsumer = added.dependencies, added.consumer
			planned, _, err := preflightCoercionsWithPolicy(&recipe, added.expression, true)
			if err != nil {
				return nil, false, alterCookingError(err)
			}
			if err := validateExpressionJoinExecution(&recipe, planned, recipe.catalog); err != nil {
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
