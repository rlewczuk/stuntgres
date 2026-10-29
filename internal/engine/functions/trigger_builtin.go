package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TriggerBuiltin retains immutable call provenance only. Ordinary SQL calls do
// not borrow a trigger activation, even when nested inside a trigger body.
type TriggerBuiltin struct {
	expr.Info
	Name string
}

func bindTriggerBuiltin(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(2279).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return TriggerBuiltin{Info: info, Name: name}, nil
}

func (n TriggerBuiltin) Validate() error {
	if n.Usage.Type == nil || n.Usage.Type.ID() != 2279 {
		return fmt.Errorf("stuntgres: invalid builtin trigger result")
	}
	switch n.Name {
	case "suppress_redundant_updates_trigger", "tsvector_update_trigger", "tsvector_update_trigger_column":
		return nil
	}
	return fmt.Errorf("stuntgres: invalid builtin trigger recipe")
}
func (n TriggerBuiltin) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Name == "suppress_redundant_updates_trigger" {
		return sqltype.Value{}, sqlError("39P01", "suppress_redundant_updates_trigger: must be called as trigger")
	}
	return sqltype.Value{}, sqlError("XX000", "tsvector_update_trigger: not fired by trigger manager")
}
func (n TriggerBuiltin) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (TriggerBuiltin) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (TriggerBuiltin) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (TriggerBuiltin) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (TriggerBuiltin) LooksLikeFunction() bool                 { return true }
func (TriggerBuiltin) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (TriggerBuiltin) SourceAdmission() (bool, bool)           { return true, false }
func (TriggerBuiltin) Children() []expr.Expr                   { return nil }
func (TriggerBuiltin) GroupLeaf()                              {}
func (n TriggerBuiltin) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	v, ok := other.(TriggerBuiltin)
	return ok && n.Name == v.Name
}
func (n TriggerBuiltin) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid builtin trigger children")
	}
	n.Info = info
	return n, nil
}
func (n TriggerBuiltin) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	v := next.(TriggerBuiltin)
	return v, v.Validate()
}
func (n TriggerBuiltin) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "TriggerBuiltin", Form: expr.FormCall, Name: n.Name}, nil
}
func (TriggerBuiltin) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n TriggerBuiltin) ValidateStoredProvider() error { return n.Validate() }
