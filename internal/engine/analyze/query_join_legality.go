package analyze

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// FirstNullableQueryMark returns the earlier of two nullable-side marks.
func FirstNullableQueryMark(a, b plan.RowMark) plan.RowMark {
	if a.Mode == 0 {
		return b
	}
	if b.Mode != 0 && b.Order < a.Order {
		return b
	}
	return a
}

// NullableQueryRowMark is the first row mark under node, which an outer join
// makes nullable; a CTE is opaque.
func NullableQueryRowMark(node plan.Node, marks []plan.RowMark, inherited plan.RowMark) plan.RowMark {
	switch n := node.(type) {
	case *plan.SourceNode:
		return QueryOccurrenceMark(marks, n.Occurrence, inherited)
	case *plan.DerivedNode:
		if n.CTE != nil {
			return plan.RowMark{}
		}
		return NullableQueryRowMark(n.Query.Root, marks, inherited)
	case *plan.JoinNode:
		return FirstNullableQueryMark(NullableQueryRowMark(n.Left, marks, inherited), NullableQueryRowMark(n.Right, marks, inherited))
	case *plan.RowLockNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.LimitNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.EvaluationNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.FilterNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.ProjectNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.SortNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.AggregateNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	case *plan.SetProjectNode:
		return NullableQueryRowMark(n.Child, marks, inherited)
	}
	return plan.RowMark{}
}

// Every key is an effective equality. Keep only its ordered, bound operands:
// the original predicate/provider remains untouched, including under NOT.
type FullJoinKey struct {
	left, right expr.Expr
}

// FullJoinFacts summarizes a FULL JOIN condition for planning: a constant
// truth, or its merge/hash-joinable keys.
type FullJoinFacts struct {
	Constant bool
	Truth    plan.Truth
	Keys     []FullJoinKey
}

// Keys are actual conjuncts, not merely equality syntax somewhere in the tree.
// OR retains only structurally common keys in the same operand order. NOT is
// pushed through Boolean nodes with SQL three-valued truth. At the resulting
// match boundary, FALSE and UNKNOWN both reject a pair.
func FullJoinCondition(c *exec.Context, join *plan.JoinNode, bound expr.Expr, negate bool) (FullJoinFacts, error) {
	if bound == nil {
		return FullJoinFacts{Constant: true, Truth: plan.True}, nil
	}
	p := bound.Properties()
	// Pruning preserves analyzed dependencies even on a physical literal.
	// Those properties still govern metadata/ACLs, not its planning truth.
	_, literal := bound.(plan.LiteralExpr)
	if literal || p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion {
		value, err := bound.Eval(c)
		if err != nil {
			return FullJoinFacts{}, err
		}
		truth, err := plan.TruthOf(value)
		if negate && truth != plan.Unknown {
			if truth == plan.True {
				truth = plan.False
			} else {
				truth = plan.True
			}
		}
		return FullJoinFacts{Constant: true, Truth: truth}, err
	}
	if e, ok := bound.(plan.LogicalExpr); ok {
		if e.Op == parser.LogicalNot {
			return FullJoinCondition(c, join, e.Left, !negate)
		}
		a, err := FullJoinCondition(c, join, e.Left, negate)
		if err != nil {
			return FullJoinFacts{}, err
		}
		b, err := FullJoinCondition(c, join, e.Right, negate)
		if err != nil {
			return FullJoinFacts{}, err
		}
		and := e.Op == parser.LogicalAnd
		if negate {
			and = !and
		}
		if a.Constant && b.Constant {
			truth := plan.Unknown
			if and {
				if a.Truth == plan.False || b.Truth == plan.False {
					truth = plan.False
				} else if a.Truth == plan.True && b.Truth == plan.True {
					truth = plan.True
				}
			} else {
				if a.Truth == plan.True || b.Truth == plan.True {
					truth = plan.True
				} else if a.Truth == plan.False && b.Truth == plan.False {
					truth = plan.False
				}
			}
			return FullJoinFacts{Constant: true, Truth: truth}, nil
		}
		if and {
			if a.Constant && a.Truth == plan.False || b.Constant && b.Truth == plan.False {
				return FullJoinFacts{Constant: true, Truth: plan.False}, nil
			}
			if a.Constant && a.Truth == plan.True {
				return b, nil
			}
			if b.Constant && b.Truth == plan.True {
				return a, nil
			}
			return FullJoinFacts{Keys: append(a.Keys, b.Keys...)}, nil
		}
		if a.Constant && a.Truth == plan.True || b.Constant && b.Truth == plan.True {
			return FullJoinFacts{Constant: true, Truth: plan.True}, nil
		}
		if a.Constant && a.Truth != plan.True {
			return b, nil
		}
		if b.Constant && b.Truth != plan.True {
			return a, nil
		}
		var common []FullJoinKey
		binder := Binder{}
		for _, left := range a.Keys {
			for _, right := range b.Keys {
				if binder.SameGroupExpression(left.left, right.left) && binder.SameGroupExpression(left.right, right.right) {
					common = append(common, left)
					break
				}
			}
		}
		return FullJoinFacts{Keys: common}, nil
	}
	if e, ok := bound.(plan.CaseExpr); ok {
		var test sqltype.Value
		if e.Test != nil {
			p := e.Test.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
				return FullJoinFacts{}, nil
			}
			var err error
			test, err = e.Test.Eval(c)
			if err != nil {
				return FullJoinFacts{}, err
			}
		}
		for _, arm := range e.Arms {
			p := arm.Condition.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
				return FullJoinFacts{}, nil
			}
			condition, err := arm.Condition.Eval(c)
			if err != nil {
				return FullJoinFacts{}, err
			}
			match := false
			if e.Test == nil {
				truth, err := plan.TruthOf(condition)
				if err != nil {
					return FullJoinFacts{}, err
				}
				match = truth == plan.True
			} else {
				fixed, safe := plan.FixedCaseTest(arm.Test)
				if !safe || fixed.ChecksDomain() {
					return FullJoinFacts{}, nil
				}
				view, err := fixed.Apply(c.Context, test, c.Input, c.Output)
				if err != nil {
					return FullJoinFacts{}, err
				}
				if !view.IsNull() && !condition.IsNull() {
					equal, err := arm.Equality.EqualValues(c.Context, view, condition, c.Input)
					if err != nil {
						return FullJoinFacts{}, err
					}
					match = equal
				}
			}
			if match {
				return FullJoinCondition(c, join, arm.Result, negate)
			}
		}
		return FullJoinCondition(c, join, e.Otherwise, negate)
	}
	if e, ok := bound.(plan.ConditionalExpr); ok && e.Kind == parser.Coalesce {
		for _, arg := range e.Args {
			p := arg.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
				// Dropping preceding constant NULLs still leaves COALESCE,
				// not a bare equality key, even with one remaining argument.
				break
			}
			value, err := arg.Eval(c)
			if err != nil {
				return FullJoinFacts{}, err
			}
			if !value.IsNull() {
				return FullJoinCondition(c, join, arg, negate)
			}
		}
	}
	if e, ok := bound.(plan.ComparisonExpr); ok && fullJoinComparisonOperator(e.Operator, negate) == "=" {
		left, err := plan.JoinExpressionSides(e.Left, join.LeftWidth, join.RightWidth)
		if err != nil {
			return FullJoinFacts{}, err
		}
		right, err := plan.JoinExpressionSides(e.Right, join.LeftWidth, join.RightWidth)
		if err != nil {
			return FullJoinFacts{}, err
		}
		if e.Left.Properties().Volatility != expr.Volatile && e.Right.Properties().Volatility != expr.Volatile && (left == 1 && right == 2 || left == 2 && right == 1) {
			return FullJoinFacts{Keys: []FullJoinKey{{left: e.Left, right: e.Right}}}, nil
		}
	}
	return FullJoinFacts{}, nil
}

// Bound ordinary comparisons have these SQL negators. This is a planning
// summary only: never replace the runtime comparison or its analyzed metadata.
func fullJoinComparisonOperator(operator string, negate bool) string {
	if !negate {
		return operator
	}
	switch operator {
	case "=":
		return "<>"
	case "<>":
		return "="
	case "<":
		return ">="
	case "<=":
		return ">"
	case ">":
		return "<="
	case ">=":
		return "<"
	}
	return ""
}
