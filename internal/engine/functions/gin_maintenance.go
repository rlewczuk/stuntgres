package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type GinMaintenanceRuntime interface{ GinCleanPendingList(ids.OID) (int64, error) }
type GinMaintenanceCall struct {
	expr.Info
	Arg expr.Expr
}

func bindGinMaintenance(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:0]
	if len(raw.Args) == 1 {
		names = inline[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Regclass).Usage()
	if operand.Usage.Type != nil && !b.CanCoerce(operand.Usage, target, sqltype.Implicit) {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := b.CaptureTypeInput(target.Type); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
		if err != nil {
			return nil, err
		}
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, arg.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int8).Usage(), raw.Pos, properties), GinMaintenanceRoutineProvider, "gin_clean_pending_list", sqltype.Regclass)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return GinMaintenanceCall{Info: info, Arg: arg}, nil
}
func (n GinMaintenanceCall) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := value.ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(GinMaintenanceRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: GIN cleanup lacks statement authority")
	}
	pages, err := runtime.GinCleanPendingList(ids.OID(oid))
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(n.Usage.Type, pages)
}
func (n GinMaintenanceCall) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n GinMaintenanceCall) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GIN maintenance", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (GinMaintenanceCall) PlanningFold() expr.Fold              { return expr.FoldNever }
func (GinMaintenanceCall) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (GinMaintenanceCall) LooksLikeFunction() bool              { return true }
func (GinMaintenanceCall) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (GinMaintenanceCall) StrictNull() expr.Strict              { return expr.StrictAfter }
func (GinMaintenanceCall) CallCost() int                        { return 1 }
func (GinMaintenanceCall) SourceAdmission() (bool, bool)        { return true, false }
func (n GinMaintenanceCall) Validate() error {
	u := n.ResultUsage()
	if u.Type == nil || u.Type.ID() != sqltype.Int8 || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup result")
	}
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Regclass || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup operand")
	}
	if n.Properties() != expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, n.Arg.Properties()) {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup properties")
	}
	return nil
}
func (n GinMaintenanceCall) ValidateStoredProvider() error { return n.Validate() }
func (n GinMaintenanceCall) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (n GinMaintenanceCall) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(GinMaintenanceCall)
	return ok && same(n.Arg, o.Arg)
}
func (n GinMaintenanceCall) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "GinMaintenance", Inputs: n.Children(), Form: expr.FormCall, Name: "gin_clean_pending_list", Quote: expr.QuoteOnRequest, Args: []expr.RenderArg{{Expr: n.Arg}}}, nil
}
func (n GinMaintenanceCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(1, 1)
}
func (n GinMaintenanceCall) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Children(), 1, written)
}
