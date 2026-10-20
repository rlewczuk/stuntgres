package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (n BlockingInquiry) Children() []expr.Expr { return []expr.Expr{n.Arg} }

func (n BlockingInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("blocking inquiry", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	n.Property = n.Arg.Properties()
	n.Property.Volatility = expr.Volatile
	return n, nil
}

// Substitute keeps pass-supplied metadata (including grouping stage identity).
// Unlike conditional reconstruction it does not recompute child properties.
func (n BlockingInquiry) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("blocking inquiry", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}

func (BlockingInquiry) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (BlockingInquiry) PlanningFold() expr.Fold              { return expr.FoldNever }
func (BlockingInquiry) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (BlockingInquiry) LooksLikeFunction() bool              { return true }

func (n BlockingInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(BlockingInquiry)
	return ok && n.Kind == o.Kind && same(n.Arg, o.Arg)
}

func (n BlockingInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: fmt.Sprintf("BlockingInquiry %d", n.Kind), Inputs: []expr.Expr{n.Arg}, Form: expr.FormCall, Name: n.Kind.Name(), Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: n.Arg}}}, nil
}

func (n BlockingInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
