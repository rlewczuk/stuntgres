package plan

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type ArrayCastExpr struct {
	expr.Info
	Child, Element expr.Expr
	Mode           sqltype.CoercionContext
	Context        string
}

func (n ArrayCastExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalArrayCast(ArrayCastExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("missing array cast runtime")
	}
	return runtime.EvalArrayCast(n)
}
func (n ArrayCastExpr) Children() []expr.Expr { return []expr.Expr{n.Child, n.Element} }
func (n ArrayCastExpr) Rebuild(info expr.Info, args []expr.Expr) (expr.Expr, error) {
	if len(args) != 2 || args[0] == nil || args[1] == nil {
		return nil, fmt.Errorf("invalid array cast children")
	}
	n.Info, n.Child, n.Element = info, args[0], args[1]
	return n, n.ValidateStoredProvider()
}
func (n ArrayCastExpr) ValidateStoredProvider() error {
	if n.Child == nil || n.Element == nil || !n.Child.ResultUsage().Type.IsArray() || !n.Usage.Type.IsArray() || n.Element.ResultUsage().Type != n.Usage.Type.Element() {
		return fmt.Errorf("invalid selected array cast")
	}
	count := 0
	valid := true
	WalkExpression(n.Element, func(node expr.Expr) {
		if input, ok := node.(ArrayCastElementExpr); ok {
			count++
			valid = valid && input.Usage.Type == n.Child.ResultUsage().Type.Element()
		}
	})
	if !valid || count != 1 {
		return fmt.Errorf("invalid array cast input ownership")
	}
	return nil
}
func (ArrayCastExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (ArrayCastExpr) PlanningFold() expr.Fold              { return expr.FoldNever }

func (n ArrayCastExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ArrayCastExpr)
	return ok && n.Usage == o.Usage && n.Mode == o.Mode && n.Context == o.Context &&
		same(n.Child, o.Child) && same(n.Element, o.Element)
}

// ArrayCastElementExpr reads only the surrounding element-map frame, not SQL parameters.
type ArrayCastElementExpr struct{ expr.Info }

func (n ArrayCastElementExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		CastArrayElement(sqltype.Usage) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("missing array element frame")
	}
	return runtime.CastArrayElement(n.Usage)
}
func (n ArrayCastElementExpr) Children() []expr.Expr { return nil }
func (n ArrayCastElementExpr) Rebuild(info expr.Info, args []expr.Expr) (expr.Expr, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("invalid array element children")
	}
	n.Info = info
	return n, nil
}
func (n ArrayCastElementExpr) ValidateStoredProvider() error {
	if n.Usage.Type == nil || !n.Usage.Valid() || !n.Usage.Type.Concrete() {
		return fmt.Errorf("invalid stored array element frame")
	}
	return nil
}
func (ArrayCastElementExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (ArrayCastElementExpr) PlanningFold() expr.Fold              { return expr.FoldNever }

func (n ArrayCastElementExpr) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ArrayCastElementExpr)
	return ok && n.Usage == o.Usage
}

func (ArrayCastExpr) ClosureTransparent() {}
