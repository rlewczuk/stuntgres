package engine

import "stuntgres/internal/engine/expr"

// Match PostgreSQL's lexical PARAM_MULTIEXPR check at actual NEW substitution.
// A scalar SubLink is a graph boundary, not a reason to inspect its query here.
func validateRuleNewUpdateOutput(recipe expr.Expr) error {
	found := false
	walkBoundExpression(recipe, func(node expr.Expr) {
		switch n := node.(type) {
		case updateOutputExpression:
			found = true
		case subqueryExpression:
			found = found || n.UpdateWidth != 0
		}
	})
	if found {
		return sqlError("0A000", "NEW variables in ON UPDATE rules cannot reference columns that are part of a multiple assignment in the subject UPDATE command")
	}
	return nil
}
