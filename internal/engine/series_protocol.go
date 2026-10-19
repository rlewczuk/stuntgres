package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e seriesExpression) Children() []expr.Expr { return e.args }

func (e seriesExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	if info.Usage.Type == nil || len(children) != 2 && len(children) != 3 {
		return nil, fmt.Errorf("stuntgres: invalid series shape")
	}
	id := info.Usage.Type.ID()
	if id != sqltype.Int4 && id != sqltype.Int8 && id != sqltype.Numeric {
		return nil, fmt.Errorf("stuntgres: invalid series result")
	}
	for _, child := range children {
		if child.ResultUsage().Type != info.Usage.Type {
			return nil, fmt.Errorf("stuntgres: invalid series argument generation")
		}
	}
	return next, nil
}

func (e seriesExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.args) {
		return nil, fmt.Errorf("stuntgres: invalid generate_series arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil generate_series argument")
		}
	}
	e.Info, e.args = info, children
	return e, nil
}

func (e seriesExpression) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e seriesExpression) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e seriesExpression) PreflightFold() expr.Preflight        { return expr.PreflightChildren }

func (e seriesExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(seriesExpression)
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

func (e seriesExpression) Render() (expr.Rendering, error) {
	tree, name := "Series generate_series", "generate_series"
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
