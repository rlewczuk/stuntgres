package engine

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A set call is analyzed as an expression, but only a cardinality-changing
// query stage may execute it. Scalar evaluation cannot consume a first item.
type regexpSetExpression struct{ regexpCall }

func newRegexpSetExpression(call regexpCall) BoundExpr { return regexpSetExpression{call} }
func (e regexpSetExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted regular expression set call")
}
func (e regexpSetExpression) EvalSet(c *EvalContext) ([]sqltype.Value, error) {
	return e.evaluateSet(c)
}

// Lexical traversal deliberately stops at expression-owned query boundaries.
func regexpSetDepth(expr BoundExpr) int {
	depth := 0
	boundExpressionChildren(expr, func(child BoundExpr) {
		if d := regexpSetDepth(child); d > depth {
			depth = d
		}
	})
	if _, ok := expr.(regexpSetExpression); ok {
		depth++
	}
	return depth
}
func containsRegexpSet(expr BoundExpr) bool { return regexpSetDepth(expr) != 0 }

// Placement checks run on analyzed expressions, even inside dead branches.
// Nested subqueries own their own placement checks and are not entered here.
func rejectRegexpSets(expr BoundExpr, context string, lateralHint bool) error {
	var first BoundExpr
	walkBoundExpression(expr, func(node BoundExpr) {
		if _, ok := node.(regexpSetExpression); ok && (first == nil || node.SourcePosition().Offset < first.SourcePosition().Offset) {
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

func validateRegexpFromCall(call regexpCall) error {
	var first BoundExpr
	for _, arg := range call.args {
		walkBoundExpression(arg, func(node BoundExpr) {
			if isQuerySet(node) && (first == nil || node.SourcePosition().Offset < first.SourcePosition().Offset) {
				first = node
			}
		})
	}
	if first == nil {
		return nil
	}
	return expressionError("0A000", "set-returning functions must appear at top level of FROM", first.SourcePosition())
}
