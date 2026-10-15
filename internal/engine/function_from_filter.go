package engine

import "stuntgres/internal/parser"

// A left-only WHERE restriction belongs before a correlated function invocation:
// excluded predecessor rows must not open its cursor. Move the complete predicate
// once in an execution-private copy, without changing canonical/view literal
// paths, outer joins, merged columns or expression boundaries. Nil declines.
func functionFromWhere(root LogicalNode, predicate BoundExpr) *joinNode {
	join, ok := root.(*joinNode)
	if !ok || join.kind != parser.JoinInner || !join.correlatedRight || len(join.merges) != 0 {
		return nil
	}
	if _, ok := join.right.(*functionFromNode); !ok {
		return nil
	}
	properties := predicate.Properties()
	if properties.Volatility == Volatile || properties.StageDependent || properties.DomainDependent || properties.RuntimeDomainConversion {
		return nil
	}
	currentFrame := true
	walkBoundExpression(predicate, func(expr BoundExpr) {
		if column, ok := expr.(columnExpression); ok && (column.frame != join.frame || column.virtual != nil) {
			currentFrame = false
		}
	})
	if !currentFrame {
		return nil
	}
	sides, err := joinExpressionSides(predicate, join.leftWidth, join.rightWidth)
	if err != nil || sides != 1 {
		// An unsupported dependency form is not a SQL failure or permission
		// to reorder it; the original post-join filter remains authoritative.
		return nil
	}
	copy := *join
	copy.left = &filterNode{querySchema: join.left.Schema(), child: join.left, predicate: predicate}
	return &copy
}
