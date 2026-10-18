package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
)

// Placement checks run on analyzed expressions, even inside dead branches.
// Nested subqueries own their own placement checks and are not entered here.
func rejectRegexpSets(bound expr.Expr, context string, lateralHint bool) error {
	var first expr.Expr
	walkBoundExpression(bound, func(node expr.Expr) {
		if isQuerySet(node) && (first == nil || node.SourcePosition().Offset < first.SourcePosition().Offset) {
			first = node
		}
	})
	if first == nil {
		return nil
	}
	err := expr.ErrorAt("0A000", "set-returning functions are not allowed in "+context, first.SourcePosition())
	if lateralHint {
		err.(*sqlstate.Error).Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	return err
}
