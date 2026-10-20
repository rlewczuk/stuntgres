package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
)

// sqlJSONResult retains enclosing casts at a protocol constructor's
// representation boundary. Ordinary callable spelling conveys no such identity.
func sqlJSONResult(node expr.Expr) bool {
	if conversion, ok := node.(coercionExpression); ok && conversion.Plan.IsJSONReturning() {
		node = conversion.Child
	}
	renderable, ok := node.(expr.Renderable)
	if !ok {
		return false
	}
	spec, err := renderable.Render()
	return err == nil && spec.JSONConstructor
}

// jsonReturningRendering asks the child for its existing syntax description,
// never its result type or raw RETURNING spelling. A missing capability leaves
// the conversion to the ordinary engine cast path.
func jsonReturningRendering(node expr.Expr) (expr.Rendering, bool, error) {
	renderable, ok := node.(expr.Renderable)
	if !ok {
		return expr.Rendering{}, false, nil
	}
	spec, err := renderable.Render()
	return spec, spec.JSONConstructor && spec.JSONReturning, err
}

func sqlJSONSource(arg expr.Expr) expr.Expr {
	if conversion, ok := arg.(coercionExpression); ok && conversion.Plan.IsJSONFormattedInput() {
		return conversion.Child
	}
	return arg
}

func jsonPredicateRecipe(child, kind string, unique, not bool) string {
	return "(" + child + functions.JSONPredicateSuffix(kind, unique, not)
}
