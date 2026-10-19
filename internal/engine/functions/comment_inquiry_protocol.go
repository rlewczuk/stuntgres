package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (n CommentInquiry) Children() []expr.Expr { return n.Args }
func (n CommentInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("comment inquiry", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, children
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n CommentInquiry) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (CommentInquiry) PlanningFold() expr.Fold                { return expr.FoldNever }
func (CommentInquiry) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (CommentInquiry) LooksLikeFunction() bool                { return true }
func (CommentInquiry) CallCost() int                          { return 100 }
func (CommentInquiry) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, false, false
}
func (n CommentInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(CommentInquiry)
	return ok && n.Kind == o.Kind && sameChildren(n.Args, o.Args, same)
}
func (n CommentInquiry) name() string {
	if n.Kind == ColumnDescription {
		return "col_description"
	}
	if n.Kind == SharedDescription {
		return "shobj_description"
	}
	return "obj_description"
}
func (n CommentInquiry) Validate() error {
	arity, second := 2, sqltype.Name
	switch n.Kind {
	case ColumnDescription:
		second = sqltype.Int4
	case ObjectDescription:
	case LegacyDescription:
		arity = 1
	case SharedDescription:
	default:
		return fmt.Errorf("stuntgres: invalid stored comment inquiry kind")
	}
	u := n.ResultUsage()
	if len(n.Args) != arity || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid stored comment inquiry signature")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range n.Args {
		id := sqltype.Oid
		if i == 1 {
			id = second
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid stored comment inquiry argument")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored comment inquiry properties")
	}
	return nil
}
func (n CommentInquiry) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: fmt.Sprintf("CommentInquiry %d", n.Kind), Inputs: n.Args, Form: expr.FormCall, Name: n.name(), Args: args, Replay: expr.Replay{Validate: n.Validate, RelationOnly: true, CatalogSnapshot: true}}, nil
}
func (n CommentInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: len(n.Args), Args: n.Args}, true, nil
}
