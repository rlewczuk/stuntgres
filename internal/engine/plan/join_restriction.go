package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// InnerJoinRestriction moves the WHERE conditions that read only the left
// input of an inner join below the join, as PostgreSQL evaluates a condition
// on one relation at that relation's scan: rows it rejects are never paired,
// and a correlated right input never runs for them. Conditions that are
// volatile, read the right input or another frame, or have an unsupported
// form stay in a residual filter above the join. The result is an
// execution-private copy: the canonical and view trees are unchanged, and
// ScanOwner keeps the original join's foreign-scan identity. Nil declines.
func InnerJoinRestriction(root Node, predicate expr.Expr) Node {
	join, ok := root.(*JoinNode)
	if !ok || join.Kind != parser.JoinInner || len(join.Merges) != 0 {
		return nil
	}
	before, after := PartitionJoinConjuncts(join, predicate)
	if before == nil {
		return nil
	}
	copy := *join
	if copy.ScanOwner == nil {
		copy.ScanOwner = join
	}
	copy.Left = &FilterNode{QuerySchema: join.Left.Schema(), Child: join.Left, Predicate: before}
	if after == nil {
		return &copy
	}
	return &FilterNode{QuerySchema: join.Schema(), Child: &copy, Predicate: after}
}

// PartitionJoinConjuncts splits a top-level conjunction into the conjuncts
// JoinLeftRestriction admits and the rest; any other expression moves or stays
// whole. Ordinary Boolean evaluation of each part is unchanged.
func PartitionJoinConjuncts(join *JoinNode, predicate expr.Expr) (before, after expr.Expr) {
	if logical, ok := predicate.(LogicalExpr); ok && logical.Op == parser.LogicalAnd && !logical.QueryRight {
		leftBefore, leftAfter := PartitionJoinConjuncts(join, logical.Left)
		rightBefore, rightAfter := PartitionJoinConjuncts(join, logical.Right)
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
	if JoinLeftRestriction(join, predicate) {
		return predicate, nil
	}
	return nil, predicate
}

// JoinLeftRestriction reports whether predicate may run on the join's left
// input instead of on joined rows: it reads only left columns of the join's own
// frame, and its evaluation properties do not depend on where it runs.
func JoinLeftRestriction(join *JoinNode, predicate expr.Expr) bool {
	if logical, ok := predicate.(LogicalExpr); ok && logical.QueryRight {
		return false
	}
	properties := predicate.Properties()
	if properties.Volatility == expr.Volatile || properties.StageDependent || properties.DomainDependent || properties.RuntimeDomainConversion {
		return false
	}
	currentFrame := true
	WalkExpression(predicate, func(expr expr.Expr) {
		if column, ok := expr.(ColumnExpr); ok && (column.Frame != join.Frame || column.Virtual != nil) {
			currentFrame = false
		}
	})
	if !currentFrame {
		return false
	}
	sides, err := JoinExpressionSides(predicate, join.LeftWidth, join.RightWidth)
	if err != nil || sides != 1 {
		// An unsupported dependency form is not a SQL failure or permission
		// to reorder it; the original post-join filter remains authoritative.
		return false
	}
	return true
}

// Slots, including descendant synthetic merged slots, belong to child frames.
// Physical origins are deliberately irrelevant to FULL path capability.
func JoinExpressionSides(node expr.Expr, leftWidth, rightWidth int) (uint8, error) {
	if node == nil {
		return 0, nil
	}
	if e, ok := node.(ColumnExpr); ok {
		if e.Slot < 0 || e.Slot >= leftWidth+rightWidth {
			return 0, fmt.Errorf("stuntgres: invalid join expression slot")
		}
		if e.Slot < leftWidth {
			return 1, nil
		}
		return 2, nil
	}
	var result uint8
	child := func(e expr.Expr) error {
		side, err := JoinExpressionSides(e, leftWidth, rightWidth)
		result |= side
		return err
	}
	var children []expr.Expr
	switch e := node.(type) {
	case LiteralExpr, ParameterExpr:
		return 0, nil
	case RowExpr:
		children = e.Fields
	case FieldExpr:
		return JoinExpressionSides(e.Base, leftWidth, rightWidth)
	case ArrayExpr:
		children = e.Elements
	case SubscriptExpr:
		sides, err := JoinExpressionSides(e.Base, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		for _, s := range e.Subscripts {
			for _, child := range [2]expr.Expr{s.Lower, s.Upper} {
				if child == nil {
					continue
				}
				side, err := JoinExpressionSides(child, leftWidth, rightWidth)
				if err != nil {
					return 0, err
				}
				sides |= side
			}
		}
		return sides, nil
	case CoercionExpr:
		return JoinExpressionSides(e.Child, leftWidth, rightWidth)
	case CollationExpr:
		return JoinExpressionSides(e.Child, leftWidth, rightWidth)
	case SelectedOperatorExpr:
		return JoinExpressionSides(e.Provider, leftWidth, rightWidth)
	case ArithmeticExpr:
		children = []expr.Expr{e.Left, e.Right}
	case BitwiseExpr:
		children = []expr.Expr{e.Left, e.Right}
	case RowComparisonExpr:
		var sides uint8
		for _, pair := range e.Comparisons {
			left, err := JoinExpressionSides(pair.Left, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			right, err := JoinExpressionSides(pair.Right, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= left | right
		}
		return sides, nil
	case ScalarArrayComparisonExpr:
		sides, err := JoinExpressionSides(e.Left, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		if e.Array != nil {
			side, err := JoinExpressionSides(e.Array, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		for _, item := range e.Items {
			side, err := JoinExpressionSides(item, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		return sides, nil
	case ComparisonExpr:
		children = []expr.Expr{e.Left, e.Right}
	case LogicalExpr:
		children = []expr.Expr{e.Left, e.Right}
	case DistinctExpr:
		children = []expr.Expr{e.Left, e.Right}
	case LikeExpr:
		children = []expr.Expr{e.Left, e.Right}
	case NullTestExpr:
		children = []expr.Expr{e.Child}
	case TruthTestExpr:
		children = []expr.Expr{e.Child}
	case ConditionalExpr:
		children = e.Args
	case ExtensionPathsExpr:
		return JoinExpressionSides(e.Arg, leftWidth, rightWidth)
	case InstalledSoundexExpr:
		children = e.Args
	case InstalledMetaphoneExpr:
		children = e.Args
	case InstalledLevenshteinExpr:
		children = e.Args
	case InstalledUUIDOsspExpr:
		children = e.Args
	case InstalledCryptoExpr:
		children = e.Args
	case InstalledTextExpr:
		children = []expr.Expr{e.Arg}
	case CaseExpr:
		if err := child(e.Test); err != nil {
			return 0, err
		}
		if err := child(e.Otherwise); err != nil {
			return 0, err
		}
		for _, arm := range e.Arms {
			if err := child(arm.Condition); err != nil {
				return 0, err
			}
			if err := child(arm.Test); err != nil {
				return 0, err
			}
			if err := child(arm.Result); err != nil {
				return 0, err
			}
		}
		return result, nil
	case expr.Composite:
		// Last, so the explicit cases above keep precedence over embedded nodes.
		children = e.Children()
	default:
		return 0, fmt.Errorf("stuntgres: unsupported join dependency expression %T", node)
	}
	for _, e := range children {
		if err := child(e); err != nil {
			return 0, err
		}
	}
	return result, nil
}
