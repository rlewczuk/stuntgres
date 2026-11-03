package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StatsResetSharedKind retains only the released operation identity.
type StatsResetSharedKind uint8

const (
	ResetShared StatsResetSharedKind = iota
	ResetBackendStats
	ResetSLRU
)

var statsResetSharedNames = [...]string{"pg_stat_reset_shared", "pg_stat_reset_backend_stats", "pg_stat_reset_slru"}

func (k StatsResetSharedKind) Name() string {
	if int(k) >= len(statsResetSharedNames) {
		return ""
	}
	return statsResetSharedNames[k]
}
func (k StatsResetSharedKind) ArgumentType() sqltype.ID {
	if k == ResetBackendStats {
		return sqltype.Int4
	}
	return sqltype.Text
}

type StatsResetShared struct {
	expr.Info
	Kind     StatsResetSharedKind
	Argument expr.Expr
}
type StatsResetSharedRuntime interface {
	StatsResetShared(StatsResetSharedKind, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindStatsResetShared(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := StatsResetSharedKind(len(statsResetSharedNames))
	for i, n := range statsResetSharedNames {
		if name == n {
			kind = StatsResetSharedKind(i)
			break
		}
	}
	required := 0
	labels := []string{"target"}
	if kind == ResetBackendStats {
		required = 1
		labels = []string{""}
	}
	_, valid := namedSlots(raw, labels, required)
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(kind.ArgumentType()).Usage()
	if operand.Usage.Type != nil {
		if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	var argument expr.Expr
	if len(raw.Args) == 0 {
		argument = b.Literal(sqltype.Null(target.Type), raw.Pos)
	} else {
		argument, err = b.CoerceArgument(operand, CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, argument.Properties())), CatalogRoutineProvider, name, kind.ArgumentType())
	if err != nil {
		return nil, err
	}
	return StatsResetShared{Info: info, Kind: kind, Argument: argument}, nil
}
func (n StatsResetShared) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Void || n.Argument == nil || n.Argument.ResultUsage().Type == nil || n.Argument.ResultUsage().Type.ID() != n.Kind.ArgumentType() {
		return fmt.Errorf("stuntgres: invalid shared statistics reset signature")
	}
	return nil
}
func (n StatsResetShared) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Argument.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == ResetBackendStats && value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, ok := ctx.(StatsResetSharedRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing shared statistics reset runtime")
	}
	return runtime.StatsResetShared(n.Kind, value, n.Usage.Type)
}
func (StatsResetShared) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (StatsResetShared) PlanningFold() expr.Fold              { return expr.FoldNever }
func (n StatsResetShared) StrictNull() expr.Strict {
	if n.Kind == ResetBackendStats {
		return expr.StrictAfter
	}
	return expr.StrictNone
}
func (StatsResetShared) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (StatsResetShared) LooksLikeFunction() bool                 { return true }
func (StatsResetShared) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (StatsResetShared) SourceAdmission() (bool, bool)           { return true, false }
func (n StatsResetShared) Children() []expr.Expr                 { return []expr.Expr{n.Argument} }
func (n StatsResetShared) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n StatsResetShared) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(StatsResetShared)
	return ok && n.Kind == o.Kind && n.ResultUsage() == o.ResultUsage()
}
func (n StatsResetShared) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(StatsResetShared)
	return ok && n.Kind == o.Kind && same(n.Argument, o.Argument)
}
func (n StatsResetShared) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid shared statistics reset reconstruction")
	}
	n.Info, n.Argument = info, children[0]
	return n, nil
}
func (n StatsResetShared) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	v, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := v.(StatsResetShared)
	return rebuilt, rebuilt.Validate()
}
func (n StatsResetShared) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "StatsResetShared", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: []expr.RenderArg{{Expr: n.Argument}}}, nil
}
func (n StatsResetShared) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n StatsResetShared) CaptureOperand(i int) (expr.Expr, error) {
	if i != 0 {
		return nil, fmt.Errorf("stuntgres: invalid shared statistics reset capture")
	}
	return n.Argument, nil
}
func (n StatsResetShared) ValidateStoredProvider() error { return n.Validate() }
func (StatsResetShared) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.StatsResetShared")
}
func namedStatsResetShared(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if name == "pg_stat_reset_backend_stats" || !declaredTypes(declared, []sqltype.ID{sqltype.Text}) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, []string{"target"}, 0)
}
