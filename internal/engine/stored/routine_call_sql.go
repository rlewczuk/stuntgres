package stored

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Hidden default children are traversed for validation/dependency recording but
// never masquerade as written actuals in portable SQL.
func routineCallSQL(n plan.SQLRoutineExpr, name string, identifier func(string) string, render func(expr.Expr) (string, error)) (string, error) {
	if err := n.ValidateArguments(n.Args); err != nil {
		return "", err
	}
	parts := make([]string, n.WrittenCount)
	for i, arg := range n.Args {
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		if i >= n.WrittenCount {
			continue
		}
		if len(n.ArgNames) > i && n.ArgNames[i].Text != "" {
			text = identifier(n.ArgNames[i].Text) + " => " + text
		}
		if n.Variadic && i == n.WrittenCount-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
