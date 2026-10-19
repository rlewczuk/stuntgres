package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CurrentSchemas contains only the BOOL recipe; path caches and pending-TEMP
// staging belong to the executing session inquiry runtime.
type CurrentSchemas struct {
	expr.Info
	Arg expr.Expr
}

func (CurrentSchemas) ResultNullability(expr.ResultSite) (bool, bool) { return true, true }
func (n CurrentSchemas) Eval(c expr.Context) (sqltype.Value, error) {
	value, err := n.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	implicit, err := value.Boolean()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := sessionInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.CurrentSchemas(implicit, n.Usage.Type)
}
func bindCurrentSchemas(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != 1 || raw.Variadic {
		return nil, b.CallSignatureError(raw)
	}
	arg, err := b.CoerceArgument(Argument{Raw: raw.Args[0]}, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(sqltype.Bool).Usage()})
	if err != nil {
		if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
			return nil, b.CallSignatureError(raw)
		}
		return nil, err
	}
	p := arg.Properties()
	p.Volatility = max(p.Volatility, expr.Stable)
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.NameArray).Usage(), raw.Pos, p), CatalogRoutineProvider, "current_schemas", sqltype.Bool)
	if err != nil {
		return nil, err
	}
	return CurrentSchemas{info, arg}, nil
}
func (n CurrentSchemas) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n CurrentSchemas) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("current_schemas", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n CurrentSchemas) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (CurrentSchemas) PlanningFold() expr.Fold                { return expr.FoldNever }
func (CurrentSchemas) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (CurrentSchemas) LooksLikeFunction() bool                { return true }
func (n CurrentSchemas) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(CurrentSchemas)
	return ok && same(n.Arg, o.Arg)
}
func (n CurrentSchemas) Render() (expr.Rendering, error) {
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Bool || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.NameArray {
		return expr.Rendering{}, fmt.Errorf("stuntgres: invalid stored current_schemas signature")
	}
	return expr.Rendering{Tree: "CurrentSchemas", Inputs: n.Children(), Form: expr.FormCall, Name: "current_schemas", Args: []expr.RenderArg{{Expr: n.Arg}}}, nil
}
func (n CurrentSchemas) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
