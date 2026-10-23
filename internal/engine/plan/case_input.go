package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// CaseTestExpr is the lexical input of a simple CASE arm's selected conversion.
// It never evaluates the source expression and is not a SQL parameter.
type CaseTestExpr struct{ expr.Info }

func (n CaseTestExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		CaseTestValue(sqltype.Usage) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("missing CASE test frame")
	}
	return runtime.CaseTestValue(n.Usage)
}
func (n CaseTestExpr) Children() []expr.Expr { return nil }
func (n CaseTestExpr) Rebuild(info expr.Info, args []expr.Expr) (expr.Expr, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("invalid CASE test children")
	}
	n.Info = info
	return n, n.ValidateStoredProvider()
}
func (n CaseTestExpr) ValidateStoredProvider() error {
	if n.Usage.Type == nil || !n.Usage.Valid() || !n.Usage.Type.Concrete() {
		return fmt.Errorf("invalid stored CASE test frame")
	}
	return nil
}
func (CaseTestExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (CaseTestExpr) PlanningFold() expr.Fold              { return expr.FoldNever }

// EvalCaseTest scopes a saved datum, including NULL, to the retained arm recipe.
func EvalCaseTest(ctx expr.Context, template expr.Expr, value sqltype.Value) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalCaseTest(expr.Expr, sqltype.Value) (sqltype.Value, error)
	})
	if !ok || template == nil {
		return sqltype.Value{}, fmt.Errorf("missing CASE test runtime")
	}
	return runtime.EvalCaseTest(template, value)
}

// FixedCaseTest recognizes only a retained fixed recipe. Optimizers must not
// reconstruct a catalog-free substitute for a selected function/IO/array recipe.
func FixedCaseTest(template expr.Expr) (sqltype.CoercionPlan, bool) {
	switch n := template.(type) {
	case CaseTestExpr:
		p, err := sqltype.PlanCoercion(n.Usage, n.Usage, sqltype.Implicit)
		return p, err == nil
	case CoercionExpr:
		_, ok := n.Child.(CaseTestExpr)
		return n.Plan, ok
	default:
		return sqltype.CoercionPlan{}, false
	}
}

func (n CaseExpr) ValidateStoredProvider() error {
	for _, arm := range n.Arms {
		if n.Test == nil {
			if arm.Test != nil {
				return fmt.Errorf("invalid searched CASE test template")
			}
			continue
		}
		if arm.Test == nil || arm.Test.ResultUsage() != arm.TestUsage {
			return fmt.Errorf("invalid selected CASE test conversion")
		}
		count, valid := 0, true
		WalkExpression(arm.Test, func(node expr.Expr) {
			if input, ok := node.(CaseTestExpr); ok {
				count++
				valid = valid && input.Usage == n.Test.ResultUsage()
			}
		})
		if count != 1 || !valid {
			return fmt.Errorf("invalid CASE test input ownership")
		}
	}
	return nil
}
