package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// FTSCurrentConfig retains only the symbolic call and its REGCONFIG result
// descriptor. Configuration identity belongs to the executing operation.
type FTSCurrentConfig struct{ expr.Info }

func bindFTSCurrentConfig(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	// Analyze supplied operands even for a zero-argument signature, preserving
	// ordinary argument diagnostics and parameter provenance before resolution.
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(operands) != 0 || len(raw.ArgNames) != 0 || raw.Variadic {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Regconfig).Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable}), FTSQueryRoutineProvider, "get_current_ts_config")
	if err != nil {
		return nil, err
	}
	n := FTSCurrentConfig{Info: info}
	return n, n.Validate()
}

func (n FTSCurrentConfig) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Regconfig || !n.Usage.Type.Concrete() || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("invalid current text search configuration result authority")
	}
	return nil
}
func (n FTSCurrentConfig) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	body, err := n.EnterRoutine(c, nil)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	runtime, ok := c.(FTSConversionRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: current text search configuration lacks statement authority")
	}
	value, err := runtime.CurrentTextSearchConfig(n.Usage.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() || !value.ValidResult(n.Usage) {
		return sqltype.Value{}, fmt.Errorf("invalid current text search configuration value")
	}
	return value, nil
}
func (FTSCurrentConfig) Children() []expr.Expr { return nil }
func (n FTSCurrentConfig) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSCurrentConfig", children, 0); err != nil {
		return nil, err
	}
	n.Info = info
	return n, n.Validate()
}
func (n FTSCurrentConfig) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n FTSCurrentConfig) ValidateStoredProvider() error      { return n.Validate() }
func (FTSCurrentConfig) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (FTSCurrentConfig) PlanningFold() expr.Fold              { return expr.FoldNever }
func (FTSCurrentConfig) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (FTSCurrentConfig) LooksLikeFunction() bool              { return true }
func (FTSCurrentConfig) ClosureTransparent()                  {}
func (n FTSCurrentConfig) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("current text search configuration", nil, "get_current_ts_config", nil)
	spec.Namespace, spec.Replay.QuoteNamespace, spec.Replay.Validate = "pg_catalog", true, n.Validate
	return spec, nil
}
func (n FTSCurrentConfig) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(FTSCurrentConfig)
	return ok && n.Usage == o.Usage
}
func (FTSCurrentConfig) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}

var _ expr.Composite = FTSCurrentConfig{}
var _ expr.Renderable = FTSCurrentConfig{}
var _ expr.StatementJumbler = FTSCurrentConfig{}
