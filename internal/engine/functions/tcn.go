package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Tcn is an ordinary SQL call, never a borrowed trigger activation.
type Tcn struct{ expr.Info }

func bindTcn(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var arguments []Argument
	if len(raw.Args) != 0 {
		arguments = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arguments[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	if len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, arguments))
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, TcnExtension, "")
	if err != nil {
		return nil, err
	}
	if routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, nil)
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "triggered_change_notification(*) specified, but triggered_change_notification is not an aggregate function", raw.Pos)
	}
	return routine.Attach(Tcn{Info: b.Info(b.Type(2279).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile})})
}
func (n Tcn) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != 2279 || n.RoutineReference == nil {
		return fmt.Errorf("stuntgres: invalid tcn call")
	}
	return nil
}
func (n Tcn) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, sqlError("39P01", "triggered_change_notification: must be called as trigger")
}
func (n Tcn) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	_, err := n.Eval(c)
	return expr.SourceCursor{}, err
}
func (Tcn) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (Tcn) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (Tcn) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (Tcn) LooksLikeFunction() bool                 { return true }
func (Tcn) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (Tcn) SourceAdmission() (bool, bool)           { return true, false }
func (Tcn) Children() []expr.Expr                   { return nil }
func (Tcn) GroupLeaf()                              {}
func (n Tcn) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(Tcn)
	return ok && n.RoutineReference == o.RoutineReference
}
func (n Tcn) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid tcn children")
	}
	n.Info = info
	return n, nil
}
func (n Tcn) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	return node, node.(Tcn).Validate()
}
func (n Tcn) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "Tcn", Form: expr.FormCall, Name: "triggered_change_notification"}, nil
}
func (Tcn) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n Tcn) ValidateStoredProvider() error { return n.Validate() }
