package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (n Advisory) Children() []expr.Expr { return n.Args }

func (n Advisory) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("advisory", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info = info
	n.Args = append([]expr.Expr(nil), children...)
	return n, nil
}

func (Advisory) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Advisory) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Advisory) StrictNull() expr.Strict              { return expr.StrictAfterBare }
func (Advisory) PreflightFold() expr.Preflight        { return expr.PreflightNullBare }
func (Advisory) LooksLikeFunction() bool              { return true }

func (n Advisory) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Advisory)
	return ok && n.Name == o.Name && sameChildren(n.Args, o.Args, same)
}

func (n Advisory) Render() (expr.Rendering, error) {
	tree := "AdvisorySession "
	if n.Lifetime == AdvisoryTransaction {
		tree = "AdvisoryTransaction "
	}
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: tree + n.Name, Inputs: n.Args, Form: expr.FormCall, Name: n.Name, Quote: expr.QuoteIdentifier, Args: args, Replay: expr.Replay{RelationOnly: true, Validate: func() error {
		if !n.ValidStored() {
			return fmt.Errorf("stuntgres: invalid stored advisory signature")
		}
		return nil
	}}}, nil
}

func (n Advisory) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(len(n.Args), len(n.Args))
}

func (n Advisory) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args, len(n.Args), written)
}
