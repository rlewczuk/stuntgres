package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TriggerDepthRuntime borrows the current invocation owner, never a bound value.
type TriggerDepthRuntime interface {
	TriggerDepthValue(*sqltype.Descriptor) (sqltype.Value, error)
}

// TriggerDepth is a zero-child live int4 inquiry with immutable provenance.
type TriggerDepth struct{ expr.Info }

func bindTriggerDepth(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
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
	if !visible || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pg_trigger_depth(*) specified, but pg_trigger_depth is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int4).Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable}), CatalogRoutineProvider, "pg_trigger_depth")
	if err != nil {
		return nil, err
	}
	return TriggerDepth{info}, nil
}

func (n TriggerDepth) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid trigger depth result type")
	}
	return nil
}
func (n TriggerDepth) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(TriggerDepthRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing trigger depth runtime")
	}
	return runtime.TriggerDepthValue(n.Usage.Type)
}
func (n TriggerDepth) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (TriggerDepth) IntrinsicVolatility() expr.Volatility    { return expr.Stable }
func (TriggerDepth) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (TriggerDepth) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (TriggerDepth) LooksLikeFunction() bool                 { return true }
func (TriggerDepth) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (TriggerDepth) SourceAdmission() (bool, bool)           { return true, false }
func (TriggerDepth) Children() []expr.Expr                   { return nil }
func (TriggerDepth) GroupLeaf()                              {}
func (TriggerDepth) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(TriggerDepth)
	return ok
}
func (n TriggerDepth) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid trigger depth children")
	}
	n.Info = info
	return n, nil
}
func (n TriggerDepth) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	v := next.(TriggerDepth)
	return v, v.Validate()
}
func (n TriggerDepth) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "TriggerDepth", Form: expr.FormCall, Name: "pg_trigger_depth"}, nil
}
func (TriggerDepth) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n TriggerDepth) ValidateStoredProvider() error { return n.Validate() }
