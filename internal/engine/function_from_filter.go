package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// A left-only WHERE restriction belongs before a correlated function invocation:
// excluded predecessor rows must not open its cursor. Move the complete predicate
// once in an execution-private copy, without changing canonical/view literal
// paths, outer joins, merged columns or expression boundaries. Nil declines.
func functionFromWhere(root LogicalNode, predicate expr.Expr) *joinNode {
	join, ok := root.(*joinNode)
	if !ok || join.Kind != parser.JoinInner || !join.CorrelatedRight || len(join.Merges) != 0 {
		return nil
	}
	if _, ok := join.Right.(*functionFromNode); !ok {
		return nil
	}
	if !functionFromRestriction(join, predicate) {
		return nil
	}
	copy := *join
	copy.Left = &filterNode{querySchema: join.Left.Schema(), Child: join.Left, Predicate: predicate}
	return &copy
}

// A conjunct can move only when its dependencies and evaluation properties
// already satisfy the whole-predicate predecessor rule.
func functionFromRestriction(join *joinNode, predicate expr.Expr) bool {
	properties := predicate.Properties()
	if properties.Volatility == expr.Volatile || properties.StageDependent || properties.DomainDependent || properties.RuntimeDomainConversion {
		return false
	}
	currentFrame := true
	walkBoundExpression(predicate, func(expr expr.Expr) {
		if column, ok := expr.(columnExpression); ok && (column.Frame != join.Frame || column.Virtual != nil) {
			currentFrame = false
		}
	})
	if !currentFrame {
		return false
	}
	sides, err := joinExpressionSides(predicate, join.LeftWidth, join.RightWidth)
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
func functionFromConjuncts(root LogicalNode, predicate expr.Expr) LogicalNode {
	join, ok := root.(*joinNode)
	if !ok || join.Kind != parser.JoinInner || len(join.Merges) != 0 {
		return nil
	}
	if _, ok := join.Right.(*functionFromNode); !ok {
		return nil
	}
	conjunction, ok := predicate.(logicalExpression)
	if !ok || conjunction.Op != parser.LogicalAnd || conjunction.QueryRight {
		return nil
	}
	before, after := partitionFunctionConjuncts(join, predicate)
	if before == nil {
		return nil
	}
	copy := *join
	copy.Left = &filterNode{querySchema: join.Left.Schema(), Child: join.Left, Predicate: before}
	if after == nil {
		return &copy
	}
	return &filterNode{querySchema: join.Schema(), Child: &copy, Predicate: after}
}

func partitionFunctionConjuncts(join *joinNode, predicate expr.Expr) (before, after expr.Expr) {
	if logical, ok := predicate.(logicalExpression); ok {
		if logical.Op != parser.LogicalAnd || logical.QueryRight {
			return nil, predicate
		}
		leftBefore, leftAfter := partitionFunctionConjuncts(join, logical.Left)
		rightBefore, rightAfter := partitionFunctionConjuncts(join, logical.Right)
		combine := func(left, right expr.Expr) expr.Expr {
			if left == nil {
				return right
			}
			if right == nil {
				return left
			}
			copy := logical
			copy.Left, copy.Right = left, right
			copy.Property = expr.MergeProperties(left.Properties(), right.Properties())
			return copy
		}
		return combine(leftBefore, rightBefore), combine(leftAfter, rightAfter)
	}
	if functionFromRestriction(join, predicate) {
		return predicate, nil
	}
	return nil, predicate
}
