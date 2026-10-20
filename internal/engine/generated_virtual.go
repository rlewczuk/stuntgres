package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Inspect logical types, never only their physical codec or simplified result.
func virtualTypeRestriction(t *sqltype.Descriptor) (domain, user bool) {
	if t == nil {
		return false, false
	}
	if t.IsDomain() {
		return true, true
	}
	if t.IsUserDefined() {
		return false, true
	}
	if t.IsArray() {
		return virtualTypeRestriction(t.Element())
	}
	return false, false
}

func virtualDestination(column catalog.Column) error {
	domain, user := virtualTypeRestriction(column.Usage.Type)
	if domain {
		return sqlError("0A000", fmt.Sprintf(`virtual generated column "%s" cannot have a domain type`, column.Name))
	}
	if user {
		err := sqlError("0A000", fmt.Sprintf(`virtual generated column "%s" cannot have a user-defined type`, column.Name)).(*sqlstate.Error)
		err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
		return err
	}
	return nil
}

func virtualGenerationSecurity(node expr.Expr) error {
	var failure error
	plan.WalkExpression(node, func(n expr.Expr) {
		if failure != nil {
			return
		}
		if _, user := virtualTypeRestriction(n.ResultUsage().Type); user {
			err := expr.ErrorAt("0A000", "generation expression uses user-defined type", n.SourcePosition()).(*sqlstate.Error)
			err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
			failure = err
		}
	})
	return failure
}

func admitColumnVirtualGeneration(c *exec.Context, e plan.ColumnExpr, view catalog.Read) error {
	if e.VirtualOwner == nil {
		return fmt.Errorf("stuntgres: missing virtual generation owner")
	}
	definition, ok := e.VirtualOwner.ColumnMeta.Generations[e.VirtualAttribute]
	if !ok || definition.Kind != catalog.GenerationVirtual || definition.Planned == nil {
		return fmt.Errorf("stuntgres: missing original virtual generation")
	}
	recipe := *c
	exec.SelectVirtualGenerationRecipe(&recipe, e.VirtualOwner, definition)
	recipe.Scope, recipe.Frame = 1, 1
	recipe.Row, recipe.Outer = nil, nil
	node, _, err := preflightCoercionsWithPolicy(&recipe, generationRecipeExpression(definition), true)
	c.PreparedDomainChecks = recipe.PreparedDomainChecks
	if err != nil {
		return err
	}
	return validateExpressionJoinExecution(&recipe, node, view)
}

// CHECK recipes belong to the completed destination, not a copied source.
func refreshGenerationChecks(d *catalog.Relation, catalog catalog.Read) error {
	for i := range d.Constraints.Check {
		next, changed, err := mapBoundExpression(catalog, d.Constraints.Check[i].Expression, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) {
			column, ok := node.(plan.ColumnExpr)
			if !ok || column.Origin.Address.ClassID != d.Address().ClassID || column.Origin.Address.ObjectID != d.Address().ObjectID {
				return node, false, nil
			}
			slot := -1
			for i, attribute := range d.Attributes {
				if attribute == column.Origin.Address.SubID {
					slot = i
					break
				}
			}
			if slot < 0 {
				return nil, false, fmt.Errorf("stuntgres: missing destination CHECK attribute")
			}
			column.Slot = slot
			column.Virtual = nil
			column.VirtualOwner, column.VirtualAttribute = nil, 0
			column.RowStart, column.RowWidth, column.QueryOccurrence = 0, 0, 0
			column = column.WithGeneration(d, slot, 0)
			return column, true, nil
		})
		if err != nil {
			return err
		}
		if changed {
			d.Constraints.Check[i].Expression = next
		}
	}
	return nil
}

func logicalColumnValue(d *catalog.Relation, slot int, row []sqltype.Value, c *exec.Context) (sqltype.Value, error) {
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[slot]]; !ok || definition.Kind != catalog.GenerationVirtual {
		return row[slot], nil
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: d.Columns[slot].Usage}, Scope: 1, Frame: 1, Slot: slot}.WithGeneration(d, slot, 0)
	local := *c
	local.Scope, local.Frame, local.Row = 1, 1, row
	return column.Eval(&local)
}

func generationInheritanceError(column string, parent, child catalog.GenerationKind) error {
	err := sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column of different kind`, column)).(*sqlstate.Error)
	err.Detail = fmt.Sprintf("Parent column is %s, child column is %s.", parent.SQL(), child.SQL())
	return err
}

func rejectVirtualIndexUses(parent *catalog.Relation, slots []int) error {
	for _, slot := range slots {
		if definition, ok := parent.ColumnMeta.Generations[parent.Attributes[slot]]; ok && definition.Kind == catalog.GenerationVirtual {
			return sqlError("0A000", "indexes on virtual generated columns are not supported")
		}
	}
	return nil
}
