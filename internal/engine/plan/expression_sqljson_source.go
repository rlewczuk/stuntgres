package plan

import (
	"stuntgres/internal/engine/expr"
)

// SQLJSONSource returns arg without its JSON FORMAT input coercion.
func SQLJSONSource(arg expr.Expr) expr.Expr {
	if conversion, ok := arg.(CoercionExpr); ok && conversion.Plan.IsJSONFormattedInput() {
		return conversion.Child
	}
	return arg
}
