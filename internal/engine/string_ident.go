package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Actuals always remain in written order. The existing Textual mask identifies
// the TEXT formal; its complement identifies the optional BOOL formal.
func stringIdentStrictArgument(raw parser.FunctionExpr, i int) bool {
	if len(raw.ArgNames) == len(raw.Args) && raw.ArgNames[i].Text != "" {
		return raw.ArgNames[i].Text == "strict"
	}
	return i == 1
}

func selectStringIdent(raw parser.FunctionExpr, visible bool, args []functions.Argument[BoundExpr], targets functions.StringTargets) (functions.StringSelection, functions.Resolution) {
	selected := functions.StringSelection{Result: sqltype.Text, Array: true}
	if !visible || raw.Variadic || len(args) < 1 || len(args) > 2 {
		return selected, functions.Missing
	}
	var assigned [2]bool
	for i, arg := range args {
		slot := i
		if len(raw.ArgNames) == len(args) && raw.ArgNames[i].Text != "" {
			switch raw.ArgNames[i].Text {
			case "str":
				slot = 0
			case "strict":
				slot = 1
			default:
				return selected, functions.Missing
			}
		}
		if assigned[slot] {
			return selected, functions.Missing
		}
		assigned[slot] = true
		selected.Textual[i] = slot == 0
		selected.Arguments[i] = sqltype.Text
		if slot == 1 {
			selected.Arguments[i] = sqltype.Bool
		}
		if arg.Usage.Type == nil {
			continue
		}
		if slot == 0 {
			if !sqltype.StringTextInput(arg.Usage.Type.BaseType().ID()) {
				return selected, functions.Missing
			}
		} else if _, err := sqltype.PlanCoercion(arg.Usage, targets.Boolean, sqltype.Implicit); err != nil {
			return selected, functions.Missing
		}
	}
	if !assigned[0] {
		return selected, functions.Missing
	}
	return selected, functions.Resolved
}
