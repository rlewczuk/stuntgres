package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (e UnnestExpr) Children() []expr.Expr { return e.Args }

func (e UnnestExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := e.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid unnest arity")
	}
	_, result := UnnestInputTypes(children[0].ResultUsage())
	if result == nil || result != info.Usage.Type {
		return nil, fmt.Errorf("stuntgres: invalid unnest generation")
	}
	return next, nil
}

func (e UnnestExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("stuntgres: invalid unnest arguments")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil unnest argument")
		}
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e UnnestExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e UnnestExpr) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e UnnestExpr) PreflightFold() expr.Preflight        { return expr.PreflightChildren }

func (e UnnestExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(UnnestExpr)
	if !ok || e.Usage != o.Usage || len(e.Args) != len(o.Args) {
		return false
	}
	if !SameIntaggIdentity(e.Procedure, e.Ref, o.Procedure, o.Ref) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

func (e UnnestExpr) Render() (expr.Rendering, error) {
	tree, name := "Unnest", "unnest"
	if e.Procedure != nil {
		tree, name = "Intagg "+e.Procedure.Name(), e.Procedure.Name()
	}
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
