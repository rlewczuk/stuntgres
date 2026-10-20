package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e SeriesExpr) Children() []expr.Expr { return e.Args }

func (e SeriesExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
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

func (e SeriesExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("stuntgres: invalid generate_series arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil generate_series argument")
		}
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e SeriesExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e SeriesExpr) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e SeriesExpr) PreflightFold() expr.Preflight        { return expr.PreflightChildren }

func (e SeriesExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(SeriesExpr)
	if !ok || e.Usage != o.Usage || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

func (e SeriesExpr) Render() (expr.Rendering, error) {
	tree, name := "Series generate_series", "generate_series"
	args := make([]expr.RenderArg, len(e.Args))
	for i, arg := range e.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{
		Tree: tree, Inputs: e.Args, Form: expr.FormCall,
		Name: name, Quote: expr.QuoteWhenNeeded, Args: args,
		Replay: expr.Replay{Validate: func() error {
			return fmt.Errorf("stuntgres: stored scalar contains set call")
		}},
	}, nil
}
