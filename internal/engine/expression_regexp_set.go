package engine

import (
	"stuntgres/internal/sqlstate"
)

// Placement checks run on analyzed expressions, even inside dead branches.
// Nested subqueries own their own placement checks and are not entered here.
func rejectRegexpSets(bound BoundExpr, context string, lateralHint bool) error {
	var first BoundExpr
	walkBoundExpression(bound, func(node BoundExpr) {
		if isQuerySet(node) && (first == nil || node.SourcePosition().Offset < first.SourcePosition().Offset) {
			first = node
		}
	})
	if first == nil {
		return nil
	}
	err := expressionError("0A000", "set-returning functions are not allowed in "+context, first.SourcePosition())
	if lateralHint {
		err.(*sqlstate.Error).Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	return err
}
