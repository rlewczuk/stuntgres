package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type bitwiseExpression struct {
	expr.Info
	left, right expr.Expr
	provider    sqltype.Bitwise
	bits        sqltype.BitStringBitwise
	network     sqltype.NetworkBitwise
	operator    sqltype.BitwiseOperator
	namespace   string
}

func (e bitwiseExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.right == nil {
		if e.Usage.Type.ID() == sqltype.Inet {
			return e.network.Unary(ctx.FunctionContext(), a)
		}
		if e.Usage.Type.ID() == sqltype.Bit {
			return e.bits.Unary(ctx.FunctionContext(), a)
		}
		return e.provider.Unary(ctx.FunctionContext(), a)
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type.ID() == sqltype.Inet {
		return e.network.Binary(ctx.FunctionContext(), a, b)
	}
	if e.Usage.Type.ID() == sqltype.Bit {
		return e.bits.Binary(ctx.FunctionContext(), a, b)
	}
	return e.provider.Binary(ctx.FunctionContext(), a, b)
}

type likeExpression struct {
	expr.Info
	left, right         expr.Expr
	provider            sqltype.LikePlan
	operator, namespace string
}

func (e likeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Evaluate(ctx.FunctionContext(), a, b)
}

type nullTestExpression struct {
	expr.Info
	child expr.Expr
	not   bool
	// DISTINCT FROM an undecorated NULL tests the whole value, even for rows.
	whole bool
}

func (e nullTestExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := v.IsNull()
	if !e.whole && !v.IsNull() && v.Type().BaseType().IsComposite() {
		result = true
		for i := range v.CompositeLen() {
			null := v.CompositeField(i).IsNull()
			if null == e.not {
				result = false
				break
			}
		}
	} else if e.not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type truthTestExpression struct {
	expr.Info
	child expr.Expr
	truth Truth
	not   bool
}

func (e truthTestExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	truth, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	result := truth == e.truth
	if e.not {
		result = !result
	}
	return sqltype.Boolean(e.Usage.Type, result), nil
}

type distinctExpression struct {
	expr.Info
	left, right expr.Expr
	equality    sqltype.ComparisonPredicate
	not         bool
}

func (e distinctExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	distinct := a.IsNull() != b.IsNull()
	if !a.IsNull() && !b.IsNull() {
		equal, err := e.equality.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(equal)
		if err != nil {
			return sqltype.Value{}, err
		}
		distinct = truth != True
	}
	if e.not {
		distinct = !distinct
	}
	return sqltype.Boolean(e.Usage.Type, distinct), nil
}

// Row predicates retain scalar pair providers, never a fabricated record datum.
// The lexical evaluator is shared with existing row/subquery predicates.
type rowComparisonExpression struct {
	expr.Info
	comparisons []comparisonExpression
	ordering    []sqltype.Comparison
	operator    string
	// Analysis-only associations retain original expanded source occurrences.
	expansions [2]map[int]compositeExpansionBinding
	// Frozen erased constructor casts retain their analyzed member usages.
	memberCasts [2]bool
}

func (e rowComparisonExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return e.compare(ctx, nil)
}

func (e rowComparisonExpression) distinct() bool {
	return e.operator == "IS DISTINCT FROM" || e.operator == "IS NOT DISTINCT FROM"
}

func (e subqueryExpression) compare(ctx expr.Context, left []sqltype.Value) (sqltype.Value, error) {
	pairs := rowComparisonExpression{Info: e.Info, comparisons: e.comparisons, ordering: e.ordering, operator: e.operator}
	return pairs.compare(ctx, left)
}

// A saved subquery tuple supplies left values. Local rows pass nil so each
// reached pair evaluates left then right before deciding whether to continue.
func (e rowComparisonExpression) compare(ctx expr.Context, left []sqltype.Value) (sqltype.Value, error) {
	unknown := false
	for i, comparison := range e.comparisons {
		var valueLeft sqltype.Value
		if left == nil {
			var err error
			valueLeft, err = comparison.left.Eval(ctx)
			if err != nil {
				return sqltype.Value{}, err
			}
		} else {
			valueLeft = left[i]
		}
		right, err := comparison.right.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.distinct() {
			distinct := valueLeft.IsNull() != right.IsNull()
			if !valueLeft.IsNull() && !right.IsNull() {
				value, err := comparison.predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
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
				return sqltype.Boolean(e.Usage.Type, e.operator == "IS DISTINCT FROM"), nil
			}
			continue
		}
		if len(e.comparisons) == 1 {
			return comparison.predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
		}
		if len(e.ordering) != 0 {
			if valueLeft.IsNull() || right.IsNull() {
				return sqltype.Null(e.Usage.Type), nil
			}
			sign, err := e.ordering[i].Compare(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if sign == 0 {
				continue
			}
			truth := sign < 0
			if e.operator == ">" || e.operator == ">=" {
				truth = sign > 0
			}
			return sqltype.Boolean(e.Usage.Type, truth), nil
		}
		value, err := comparison.predicate.Evaluate(ctx.FunctionContext(), valueLeft, right, ctx.FunctionInput())
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
		if e.operator == "<>" {
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
	truth := e.operator == "=" || e.operator == "<=" || e.operator == ">=" || e.operator == "IS NOT DISTINCT FROM"
	return sqltype.Boolean(e.Usage.Type, truth), nil
}
