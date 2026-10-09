package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Added defaults are execution-private. Only captured nonvolatile values enter
// transaction shape projections; persistent definitions retain their bound tree.
type addedColumn struct {
	attribute   int32
	column      *parser.Column
	declaration createColumnDeclaration
	expression  BoundExpr
	value       sqltype.Value
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

func (e *Engine) addColumnCandidate(scope *StatementScope, op *Operation, auth AuthorizationView, old *relationDescriptor, action parser.AlterTableAction) (*relationDescriptor, addedColumn, error) {
	fail := func(err error) (*relationDescriptor, addedColumn, error) { return nil, addedColumn{}, err }
	column := action.Definition
	if column == nil {
		return fail(fmt.Errorf("stuntgres: missing ADD column declaration"))
	}
	if isSystemColumn(column.Name) {
		return fail(sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name)))
	}
	if _, exists := old.index[column.Name]; exists {
		message := fmt.Sprintf(`column "%s" of relation "%s" already exists`, column.Name, old.name)
		if !action.IfNotExists {
			return fail(sqlError("42701", message))
		}
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: message + ", skipping"})
		return old, addedColumn{}, nil
	}
	if old.attributeHighWater >= 1600 {
		return fail(sqlError("54011", "tables can have at most 1600 columns"))
	}
	if serialTypeName(column.TypeName) != "" {
		return fail(insertError("0A000", "ADD COLUMN serial declarations are unsupported", column.Pos))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if auth.catalog.Epoch() != e.state.epoch {
		return fail(ErrCatalogReplaced)
	}
	usage, err := e.resolveType(column.TypeName, op)
	if err != nil {
		return fail(err)
	}
	usage, err = e.resolveColumnCollation(usage, column.Collation, op)
	if err != nil {
		return fail(err)
	}
	if usage.Type.ID() == sqltype.Regclass || usage.Type.IsArray() && usage.Type.Element().ID() == sqltype.Regclass {
		return fail(typeDiagnostic(column.TypeName, "0A000", "unsupported column type"))
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
	attribute := old.attributeHighWater + 1
	next := *old
	next.attributeHighWater = attribute
	next.columns = slices.Clone(old.columns)
	next.attributes = slices.Clone(old.attributes)
	next.index = maps.Clone(old.index)
	next.defaults = maps.Clone(old.defaults)
	next.notNullConstraints = slices.Clone(old.notNullConstraints)
	next.checkConstraints = slices.Clone(old.checkConstraints)
	next.index[column.Name] = len(next.columns)
	definition := ColumnDescriptor{Name: column.Name, Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: uint32(old.address.ObjectID), OriginAttribute: int16(attribute)}
	next.columns = append(next.columns, definition)
	next.attributes = append(next.attributes, attribute)
	added := addedColumn{attribute: attribute, column: column, declaration: declaration, value: sqltype.Null(usage.Type)}
	if declaration.defaultExpr != nil {
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.queryAuth = &auth
		node, err := binder.bindDefault(declaration.defaultExpr, definition)
		if err != nil {
			return fail(alterCookingError(err))
		}
		if !structuralNull(node) {
			persisted := columnDefault{expression: node}
			if _, err := (scalarRenderer{dependencies: &persisted.dependencies, commentSnapshot: &persisted.catalogSnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
				return fail(alterCookingError(err))
			}
			if next.defaults == nil {
				next.defaults = make(map[int32]columnDefault)
			}
			next.defaults[attribute] = persisted
			added.expression = node
		}
	}
	return &next, added, nil
}

func planAddedColumns(scope *StatementScope, additions []addedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	volatile := false
	evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output}
	for i := range additions {
		added := &additions[i]
		if added.expression != nil {
			planned, _, err := preflightCoercionsWithPolicy(&evaluation, added.expression, true)
			if err != nil {
				return nil, false, alterCookingError(err)
			}
			added.expression = planned
			if planned.Properties().Volatility > Stable {
				volatile = true
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
	return fills, volatile, nil
}
