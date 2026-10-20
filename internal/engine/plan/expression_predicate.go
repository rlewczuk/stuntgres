package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type BitwiseExpr struct {
	expr.Info
	Left, Right expr.Expr
	Provider    sqltype.Bitwise
	Bits        sqltype.BitStringBitwise
	Network     sqltype.NetworkBitwise
	Operator    sqltype.BitwiseOperator
	Namespace   string
}

func (e BitwiseExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Right == nil {
		if e.Usage.Type.ID() == sqltype.Inet {
			return e.Network.Unary(ctx.FunctionContext(), a)
		}
		if e.Usage.Type.ID() == sqltype.Bit {
			return e.Bits.Unary(ctx.FunctionContext(), a)
		}
		return e.Provider.Unary(ctx.FunctionContext(), a)
	}
	b, err := e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type.ID() == sqltype.Inet {
		return e.Network.Binary(ctx.FunctionContext(), a, b)
	}
	if e.Usage.Type.ID() == sqltype.Bit {
		return e.Bits.Binary(ctx.FunctionContext(), a, b)
	}
	return e.Provider.Binary(ctx.FunctionContext(), a, b)
}

type LikeExpr struct {
	expr.Info
	Left, Right         expr.Expr
	Provider            sqltype.LikePlan
	Operator, Namespace string
}

func (e LikeExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Evaluate(ctx.FunctionContext(), a, b)
}

type NullTestExpr struct {
	expr.Info
	Child expr.Expr
	Not   bool
	// DISTINCT FROM an undecorated NULL tests the whole value, even for rows.
	Whole bool
}

func (e NullTestExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.Child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := v.IsNull()
	if !e.Whole && !v.IsNull() && v.Type().BaseType().IsComposite() {
		result = true
		for i := range v.CompositeLen() {
			null := v.CompositeField(i).IsNull()
			if null == e.Not {
				result = false
				break
			}
		}
	} else if e.Not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type TruthTestExpr struct {
	expr.Info
	Child expr.Expr
	Truth Truth
	Not   bool
}

func (e TruthTestExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.Child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	truth, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := truth == e.Truth
	if e.Not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type DistinctExpr struct {
	expr.Info
	Left, Right expr.Expr
	Equality    sqltype.ComparisonPredicate
	Not         bool
}

func (e DistinctExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	distinct := a.IsNull() != b.IsNull()
	if !a.IsNull() && !b.IsNull() {
		equal, err := e.Equality.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(equal)
		if err != nil {
			return sqltype.Value{}, err
		}
		distinct = truth != True
	}
	if e.Not {
		distinct = !distinct
	}
	return sqltype.Boolean(e.Usage.Type, distinct), nil
}

// Row predicates retain scalar pair providers, never a fabricated record datum.
// The lexical evaluator is shared with existing row/subquery predicates.
type RowComparisonExpr struct {
	expr.Info
	Comparisons []ComparisonExpr
	Ordering    []sqltype.Comparison
	Operator    string
	// Analysis-only associations retain original expanded source occurrences.
	Expansions [2]map[int]CompositeExpansionBinding
	// Frozen erased constructor casts retain their analyzed member usages.
	MemberCasts [2]bool
}

func (e RowComparisonExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return e.compare(ctx, nil)
}

func (e RowComparisonExpr) Distinct() bool {
	return e.Operator == "IS DISTINCT FROM" || e.Operator == "IS NOT DISTINCT FROM"
}

func (e SubqueryExpr) Compare(ctx expr.Context, left []sqltype.Value) (sqltype.Value, error) {
	pairs := RowComparisonExpr{Info: e.Info, Comparisons: e.Comparisons, Ordering: e.Ordering, Operator: e.Operator}
	return pairs.compare(ctx, left)
}

// A saved subquery tuple supplies left values. Local rows pass nil so each
// reached pair evaluates left then right before deciding whether to continue.
func (e RowComparisonExpr) compare(ctx expr.Context, left []sqltype.Value) (sqltype.Value, error) {
	unknown := false
	for i, comparison := range e.Comparisons {
		var valueLeft sqltype.Value
		if left == nil {
			var err error
			valueLeft, err = comparison.Left.Eval(ctx)
			if err != nil {
				return sqltype.Value{}, err
			}
		} else {
			valueLeft = left[i]
		}
		right, err := comparison.Right.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.Distinct() {
			distinct := valueLeft.IsNull() != right.IsNull()
			if !valueLeft.IsNull() && !right.IsNull() {
				value, err := comparison.Predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
				if err != nil {
					return sqltype.Value{}, err
				}
				equal, err := value.Boolean()
				if err != nil {
					return sqltype.Value{}, err
				}
				distinct = !equal
			}
			if distinct {
				return sqltype.Boolean(e.Usage.Type, e.Operator == "IS DISTINCT FROM"), nil
			}
			continue
		}
		if len(e.Comparisons) == 1 {
			return comparison.Predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
		}
		if len(e.Ordering) != 0 {
			if valueLeft.IsNull() || right.IsNull() {
				return sqltype.Null(e.Usage.Type), nil
			}
			sign, err := e.Ordering[i].Compare(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if sign == 0 {
				continue
			}
			truth := sign < 0
			if e.Operator == ">" || e.Operator == ">=" {
				truth = sign > 0
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
		value, err := comparison.Predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			unknown = true
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.Operator == "<>" {
			if truth {
				return sqltype.Boolean(e.Usage.Type, true), nil
			}
		} else if !truth {
			return sqltype.Boolean(e.Usage.Type, false), nil
		}
	}
	if unknown {
		return sqltype.Null(e.Usage.Type), nil
	}
	truth := e.Operator == "=" || e.Operator == "<=" || e.Operator == ">=" || e.Operator == "IS NOT DISTINCT FROM"
	return sqltype.Boolean(e.Usage.Type, truth), nil
}
