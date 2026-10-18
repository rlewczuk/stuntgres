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
	if !functionFromRestriction(join, predicate) {
		return nil
	}
	copy := *join
	copy.left = &filterNode{querySchema: join.left.Schema(), child: join.left, predicate: predicate}
	return &copy
}

// A conjunct can move only when its dependencies and evaluation properties
// already satisfy the whole-predicate predecessor rule.
func functionFromRestriction(join *joinNode, predicate BoundExpr) bool {
	properties := predicate.Properties()
	if properties.Volatility == Volatile || properties.StageDependent || properties.DomainDependent || properties.RuntimeDomainConversion {
		return false
	}
	currentFrame := true
	walkBoundExpression(predicate, func(expr BoundExpr) {
		if column, ok := expr.(columnExpression); ok && (column.frame != join.frame || column.virtual != nil) {
			currentFrame = false
		}
	})
	if !currentFrame {
		return false
	}
	sides, err := joinExpressionSides(predicate, join.leftWidth, join.rightWidth)
	if err != nil || sides != 1 {
		// An unsupported dependency form is not a SQL failure or permission
		// to reorder it; the original post-join filter remains authoritative.
		return false
	}
	return true
}

// functionFromConjuncts partitions only the immediate function join's WHERE
// conjunction. Volatile/unsafe residuals stay after expansion; ordinary Boolean
// evaluation and the canonical tree remain unchanged. Nil declines.
func functionFromConjuncts(root LogicalNode, predicate BoundExpr) LogicalNode {
	join, ok := root.(*joinNode)
	if !ok || join.kind != parser.JoinInner || len(join.merges) != 0 {
		return nil
	}
	if _, ok := join.right.(*functionFromNode); !ok {
		return nil
	}
	conjunction, ok := predicate.(logicalExpression)
	if !ok || conjunction.op != parser.LogicalAnd || conjunction.queryRight {
		return nil
	}
	before, after := partitionFunctionConjuncts(join, predicate)
	if before == nil {
		return nil
	}
	copy := *join
	copy.left = &filterNode{querySchema: join.left.Schema(), child: join.left, predicate: before}
	if after == nil {
		return &copy
	}
	return &filterNode{querySchema: join.Schema(), child: &copy, predicate: after}
}

func partitionFunctionConjuncts(join *joinNode, predicate BoundExpr) (before, after BoundExpr) {
	if logical, ok := predicate.(logicalExpression); ok {
		if logical.op != parser.LogicalAnd || logical.queryRight {
			return nil, predicate
		}
		leftBefore, leftAfter := partitionFunctionConjuncts(join, logical.left)
		rightBefore, rightAfter := partitionFunctionConjuncts(join, logical.right)
		combine := func(left, right BoundExpr) BoundExpr {
			if left == nil {
				return right
			}
			if right == nil {
				return left
			}
			copy := logical
			copy.left, copy.right = left, right
			copy.Property = mergeExpressionProperties(left.Properties(), right.Properties())
			return copy
		}
		return combine(leftBefore, rightBefore), combine(leftAfter, rightAfter)
	}
	if functionFromRestriction(join, predicate) {
		return predicate, nil
	}
	return nil, predicate
}
