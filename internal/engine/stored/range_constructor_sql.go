package stored

import (
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func rangeConstructorCallSQL(n plan.RangeConstructorExpr, name string, render func(expr.Expr) (string, error)) (string, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return "", err
	}
	parts := make([]string, len(n.Args))
	for i, arg := range n.Args {
		var err error
		parts[i], err = render(arg)
		if err != nil {
			return "", err
		}
		if n.Variadic && i == len(n.Args)-1 {
			parts[i] = "VARIADIC " + parts[i]
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
