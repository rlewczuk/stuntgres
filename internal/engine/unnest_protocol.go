package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e unnestExpression) Children() []expr.Expr { return e.args }

func (e unnestExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid unnest arity")
	}
	_, result := unnestInputTypes(children[0].ResultUsage())
	if result == nil || result != info.Usage.Type {
		return nil, fmt.Errorf("stuntgres: invalid unnest generation")
	}
	return next, nil
}

func (e unnestExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.args) {
		return nil, fmt.Errorf("stuntgres: invalid unnest arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil unnest argument")
		}
	}
	e.Info, e.args = info, children
	return e, nil
}

func (e unnestExpression) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e unnestExpression) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e unnestExpression) PreflightFold() expr.Preflight        { return expr.PreflightChildren }

func (e unnestExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(unnestExpression)
	if !ok || e.Usage != o.Usage || len(e.args) != len(o.args) {
		return false
	}
	if !sameIntaggIdentity(e.procedure, e.ref, o.procedure, o.ref) {
		return false
	}
	for i, arg := range e.args {
		if !same(arg, o.args[i]) {
			return false
		}
	}
	return true
}

func (e unnestExpression) Render() (expr.Rendering, error) {
	tree, name := "Unnest", "unnest"
	if e.procedure != nil {
		tree, name = "Intagg "+e.procedure.Name(), e.procedure.Name()
	}
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
