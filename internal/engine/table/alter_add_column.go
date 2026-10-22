package table

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Added defaults are execution-private. Only captured nonvolatile values enter
// transaction shape projections; persistent definitions retain their bound tree.
type AddedColumn struct {
	Attribute    int32
	Column       *parser.Column
	Declaration  analyze.ColumnDeclaration
	Expression   expr.Expr
	Dependencies []ids.ObjectRef // Borrowed cooked DEFAULT manifest, distinct from the ALTER statement.
	Consumer     *catalog.Relation
	Value        sqltype.Value
}

func AlterCookingError(err error) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position = ""
		return &copy
	}
	return err
}

// PlanAddedColumns prepares the ADD COLUMN DEFAULT recipes and captures each
// nonvolatile value once; perRow reports a DEFAULT that needs a row rewrite.
func PlanAddedColumns(evaluation exec.Context, additions []AddedColumn) (map[int32]sqltype.Value, bool, error) {
	var fills map[int32]sqltype.Value
	perRow := false
	for i := range additions {
		added := &additions[i]
		if added.Expression != nil {
			recipe := evaluation
			recipe.RoutineRecipe, recipe.RoutineGraft = true, false
			recipe.RoutineReferences, recipe.RoutineConsumer = added.Dependencies, added.Consumer
			planned, _, err := exec.PreflightCoercionsWithPolicy(&recipe, added.Expression, true)
			if err != nil {
				return nil, false, AlterCookingError(err)
			}
			if err := exec.AdmitExpression(&recipe, planned, recipe.Catalog); err != nil {
				return nil, false, AlterCookingError(err)
			}
			added.Expression = planned
			properties := planned.Properties()
			if properties.Volatility > expr.Stable || properties.RuntimeDomainConversion {
				perRow = true
				continue
			}
			added.Value, err = planned.Eval(&evaluation)
			if err != nil {
				return nil, false, AlterCookingError(err)
			}
		}
		if fills == nil {
			fills = make(map[int32]sqltype.Value, len(additions))
		}
		fills[added.Attribute] = added.Value
	}
	return fills, perRow, nil
}

// AddColumnCandidate is old with the ADD COLUMN of action, and the column's
// row-fill plan. captured is an already resolved column type (typed tables).
func AddColumnCandidate(h AlterHost, auth ddl.Authority, old *catalog.Relation, action parser.AlterTableAction, captured *sqltype.Usage) (*catalog.Relation, AddedColumn, error) {
	view := h.AccessView(auth)
	fail := func(err error) (*catalog.Relation, AddedColumn, error) { return nil, AddedColumn{}, err }
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
		h.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: message + ", skipping"})
		return old, AddedColumn{}, nil
	}
	if old.ColumnMeta.HighWater >= 1600 {
		return fail(sqlError("54011", "tables can have at most 1600 columns"))
	}
	if sequence.SerialTypeName(column.TypeName) != "" {
		return fail(insertError("0A000", "ADD COLUMN serial declarations are unsupported", column.Pos))
	}
	if view.Catalog.Epoch() != h.LiveEpoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	var usage sqltype.Usage
	var err error
	if captured != nil {
		usage = *captured
	} else {
		usage, err = h.ResolveType(column.TypeName)
		if err == nil {
			usage, err = h.ResolveColumnCollation(usage, column.Collation)
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
	added := AddedColumn{Attribute: attribute, Column: column, Declaration: declaration, Value: sqltype.Null(usage.Type)}
	node, origin := analyze.ColumnDefault(view.Catalog, &next, len(next.Columns)-1)
	added.Expression = node
	added.Dependencies, added.Consumer = origin.Definition.Dependencies, origin.Consumer
	if sqltype.ContainsDomainConstraints(usage.Type) && h.Input().TypeDiagnosticName == nil {
		if err := h.CaptureTypeDiagnosticName(auth); err != nil {
			return fail(err)
		}
	}
	if declaration.DefaultExpr != nil {
		node, err := h.BindDefault(auth, old, definition, declaration.DefaultExpr)
		if err != nil {
			return fail(AlterCookingError(err))
		}
		if !stored.OmitColumnDefault(view.Catalog, node, usage) {
			persisted := catalog.ColumnDefault{Expression: node}
			if _, err := (stored.ScalarRenderer{Definition: h.OperationDefinition(), ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Dependencies: &persisted.Dependencies, CommentSnapshot: &persisted.CatalogSnapshot, CatalogAliasReferences: &persisted.CatalogAliasReferences}).SQL(h.Context(), view.Catalog, node); err != nil {
				return fail(AlterCookingError(err))
			}
			if next.ColumnMeta.Defaults == nil {
				next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
			}
			next.ColumnMeta.Defaults[attribute] = persisted
			added.Expression = node
			added.Dependencies, added.Consumer = persisted.Dependencies, old
		}
	}
	if err := stored.RefreshGenerationChecks(&next, view.Catalog); err != nil {
		return fail(err)
	}
	return &next, added, nil
}
