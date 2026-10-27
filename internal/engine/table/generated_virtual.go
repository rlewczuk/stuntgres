package table

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

func VirtualDestination(column catalog.Column) error {
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

func VirtualGenerationSecurity(node expr.Expr) error {
	var failure error
	plan.WalkExpression(node, func(n expr.Expr) {
		if failure != nil {
			return
		}
		if _, user := virtualTypeRestriction(n.ResultUsage().Type); user {
			err := expr.ErrorAt("0A000", "generation expression uses user-defined type", n.SourcePosition()).(*sqlstate.Error)
			err.Detail = "Virtual generated columns that make use of user-defined types are not yet supported."
			failure = err
			return
		}
		call, ok := n.(interface{ CallRoutineReference() expr.RoutineReference })
		if !ok {
			return
		}
		if procedure, ok := call.CallRoutineReference().(*catalog.Procedure); ok && procedure != nil && procedure.Builtin == nil {
			err := expr.ErrorAt("0A000", "generation expression uses user-defined function", n.SourcePosition()).(*sqlstate.Error)
			err.Detail = "Virtual generated columns that make use of user-defined functions are not yet supported."
			failure = err
		}
	})
	return failure
}

func AdmitColumnVirtualGeneration(c *exec.Context, e plan.ColumnExpr, view catalog.Read) error {
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
	node, _, err := exec.PreflightCoercionsWithPolicy(&recipe, GenerationRecipeExpression(definition), true)
	c.PreparedDomainChecks = recipe.PreparedDomainChecks
	if err != nil {
		return err
	}
	return exec.AdmitExpression(&recipe, node, view)
}

func LogicalColumnValue(d *catalog.Relation, slot int, row []sqltype.Value, c *exec.Context) (sqltype.Value, error) {
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[slot]]; !ok || definition.Kind != catalog.GenerationVirtual {
		return row[slot], nil
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: d.Columns[slot].Usage}, Scope: 1, Frame: 1, Slot: slot}.WithGeneration(d, slot, 0)
	local := *c
	local.Scope, local.Frame, local.Row = 1, 1, row
	return column.Eval(&local)
}
