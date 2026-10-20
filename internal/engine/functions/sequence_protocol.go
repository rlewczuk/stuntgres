package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (n SequenceCall) Children() []expr.Expr { return n.Args }

// Rebuild keeps the provider identity: Ref, Temporary and Identity are
// catalog facts the engine's consumers remap explicitly, never children.
func (n SequenceCall) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("sequence", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info = info
	n.Args = append([]expr.Expr(nil), children...)
	return n, nil
}

func (SequenceCall) PlanningFold() expr.Fold       { return expr.FoldNever }
func (SequenceCall) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (SequenceCall) LooksLikeFunction() bool       { return true }

// SameGroup compares the bound provider and its TEMP provenance, not the
// identity marker, which grouping never sees on identity DEFAULT recipes.
func (n SequenceCall) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(SequenceCall)
	return ok && n.Name == o.Name && n.Ref == o.Ref && n.Temporary == o.Temporary && sameChildren(n.Args, o.Args, same)
}

// Render is the display form; stored scalar SQL keeps the engine's explicit
// catalog-validated sequence case and never replays this rendering.
func (n SequenceCall) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: fmt.Sprintf("Sequence %s oid %d", n.Name, n.Ref.Address.ObjectID), Inputs: n.Args, Form: expr.FormCall, Name: n.Name, Quote: expr.QuoteOnRequest, Args: args}, nil
}

func (n SequenceCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(len(n.Args), len(n.Args))
}

func (n SequenceCall) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args, len(n.Args), written)
}
