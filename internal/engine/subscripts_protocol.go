package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e subscriptsExpression) Children() []expr.Expr { return e.args }

func (e subscriptsExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	if len(children) != 2 && len(children) != 3 || info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.Int4 {
		return nil, fmt.Errorf("stuntgres: invalid subscripts shape")
	}
	if children[0].ResultUsage().Type == nil || !children[0].ResultUsage().Type.BaseType().IsArray() ||
		children[1].ResultUsage().Type == nil || children[1].ResultUsage().Type.ID() != sqltype.Int4 {
		return nil, fmt.Errorf("stuntgres: invalid subscripts arguments")
	}
	if len(children) == 3 && (children[2].ResultUsage().Type == nil || children[2].ResultUsage().Type.ID() != sqltype.Bool) {
		return nil, fmt.Errorf("stuntgres: invalid subscripts direction")
	}
	return next, nil
}

func (e subscriptsExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.args) {
		return nil, fmt.Errorf("stuntgres: invalid generate_subscripts arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil generate_subscripts argument")
		}
	}
	e.Info, e.args = info, children
	return e, nil
}

func (e subscriptsExpression) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e subscriptsExpression) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e subscriptsExpression) PreflightFold() expr.Preflight        { return expr.PreflightChildren }

func (e subscriptsExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(subscriptsExpression)
	if !ok || e.Usage != o.Usage || len(e.args) != len(o.args) {
		return false
	}
	for i, arg := range e.args {
		if !same(arg, o.args[i]) {
			return false
		}
	}
	return true
}

func (e subscriptsExpression) Render() (expr.Rendering, error) {
	tree, name := "Subscripts generate_subscripts", "generate_subscripts"
	args := make([]expr.RenderArg, len(e.args))
	for i, arg := range e.args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{
		Tree: tree, Inputs: e.args, Form: expr.FormCall,
		Name: name, Quote: expr.QuoteWhenNeeded, Args: args,
		Replay: expr.Replay{Validate: func() error {
			return fmt.Errorf("stuntgres: stored scalar contains set call")
		}},
	}, nil
}
