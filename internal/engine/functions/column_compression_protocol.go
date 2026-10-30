package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (n ColumnCompression) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n ColumnCompression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("column compression", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

func (ColumnCompression) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (ColumnCompression) PlanningFold() expr.Fold { return expr.FoldNever }
func (ColumnCompression) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (ColumnCompression) LooksLikeFunction() bool { return true }
func (ColumnCompression) CallCost() int { return 1 }
func (ColumnCompression) SourceAdmission() (bool, bool) { return true, false }
func (n ColumnCompression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ColumnCompression)
	return ok && same(n.Arg, o.Arg)
}

func (n ColumnCompression) Validate() error {
	u := n.ResultUsage()
	if u.Type == nil || u.Type.ID() != sqltype.Text || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid column compression result")
	}
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || !n.Arg.ResultUsage().Type.Concrete() {
		return fmt.Errorf("stuntgres: invalid column compression operand")
	}
	if n.Properties() != expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, n.Arg.Properties()) {
		return fmt.Errorf("stuntgres: invalid column compression properties")
	}
	return nil
}

func (n ColumnCompression) ValidateStoredProvider() error { return n.Validate() }
func (n ColumnCompression) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{
		Tree: "ColumnCompression", Inputs: []expr.Expr{n.Arg}, Form: expr.FormCall,
		Name: "pg_column_compression", Args: []expr.RenderArg{{Expr: n.Arg}},
	}, nil
}

func (n ColumnCompression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
