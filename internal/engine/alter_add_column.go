package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) addColumnCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, table.AddedColumn, error) {
	scope.routineAuth = auth
	e.mu.Lock()
	defer e.mu.Unlock()
	h := alterHost{ddlHost{e: e, op: op}, scope}
	fail := func(err error) (*catalog.Relation, table.AddedColumn, error) { return nil, table.AddedColumn{}, err }
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
		return old, table.AddedColumn{}, nil
	}
	if old.ColumnMeta.HighWater >= 1600 {
		return fail(sqlError("54011", "tables can have at most 1600 columns"))
	}
	if sequence.SerialTypeName(column.TypeName) != "" {
		return fail(insertError("0A000", "ADD COLUMN serial declarations are unsupported", column.Pos))
	}
	if auth.Catalog.Epoch() != h.LiveEpoch() {
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
	added := table.AddedColumn{Attribute: attribute, Column: column, Declaration: declaration, Value: sqltype.Null(usage.Type)}
	node, origin := analyze.ColumnDefault(auth.Catalog, &next, len(next.Columns)-1)
	added.Expression = node
	added.Dependencies, added.Consumer = origin.Definition.Dependencies, origin.Consumer
	if sqltype.ContainsDomainConstraints(usage.Type) && scope.input.TypeDiagnosticName == nil {
		if err := h.CaptureTypeDiagnosticName(auth); err != nil {
			return fail(err)
		}
	}
	if declaration.DefaultExpr != nil {
		node, err := h.BindDefault(auth, old, definition, declaration.DefaultExpr)
		if err != nil {
			return fail(table.AlterCookingError(err))
		}
		if !stored.OmitColumnDefault(auth.Catalog, node, usage) {
			persisted := catalog.ColumnDefault{Expression: node}
			if _, err := (stored.ScalarRenderer{Definition: op.Definition, ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Dependencies: &persisted.Dependencies, CommentSnapshot: &persisted.CatalogSnapshot, CatalogAliasReferences: &persisted.CatalogAliasReferences}).SQL(op.Context(), auth.Catalog, node); err != nil {
				return fail(table.AlterCookingError(err))
			}
			if next.ColumnMeta.Defaults == nil {
				next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
			}
			next.ColumnMeta.Defaults[attribute] = persisted
			added.Expression = node
			added.Dependencies, added.Consumer = persisted.Dependencies, old
		}
	}
	if err := stored.RefreshGenerationChecks(&next, auth.Catalog); err != nil {
		return fail(err)
	}
	return &next, added, nil
}

func planAddedColumns(scope *StatementScope, additions []table.AddedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	perRow := false
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: scope.routineAuth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for i := range additions {
		added := &additions[i]
		if added.Expression != nil {
			recipe := evaluation
			recipe.RoutineRecipe, recipe.RoutineGraft = true, false
			recipe.RoutineReferences, recipe.RoutineConsumer = added.Dependencies, added.Consumer
			planned, _, err := exec.PreflightCoercionsWithPolicy(&recipe, added.Expression, true)
			if err != nil {
				return nil, false, table.AlterCookingError(err)
			}
			if err := exec.AdmitExpression(&recipe, planned, recipe.Catalog); err != nil {
				return nil, false, table.AlterCookingError(err)
			}
			added.Expression = planned
			properties := planned.Properties()
			if properties.Volatility > expr.Stable || properties.RuntimeDomainConversion {
				perRow = true
				continue
			}
			added.Value, err = planned.Eval(&evaluation)
			if err != nil {
				return nil, false, table.AlterCookingError(err)
			}
		}
		if fills == nil {
			fills = make(map[int32]sqltype.Value, len(additions))
		}
		fills[added.Attribute] = added.Value
	}
	return fills, perRow, nil
}
