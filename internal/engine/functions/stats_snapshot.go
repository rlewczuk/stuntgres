package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var statsSnapshotNames = [...]string{"pg_stat_get_snapshot_timestamp", "pg_stat_clear_snapshot"}

type StatsSnapshot struct {
	expr.Info
	Clear bool
}
type StatsSnapshotRuntime interface {
	StatsSnapshotValue(bool, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindStatsSnapshot(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) > 0 && len(raw.Args) <= 100 {
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !visible || name != statsSnapshotNames[0] && name != statsSnapshotNames[1] || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	clear := name == statsSnapshotNames[1]
	id, volatility := sqltype.Timestamptz, expr.Stable
	if clear {
		id, volatility = sqltype.Void, expr.Volatile
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(id).Usage(), raw.Pos, expr.Properties{Volatility: volatility}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return StatsSnapshot{info, clear}, nil
}
func (n StatsSnapshot) Validate() error {
	id := sqltype.Timestamptz
	if n.Clear {
		id = sqltype.Void
	}
	if n.Usage.Type == nil || n.Usage.Type.ID() != id {
		return fmt.Errorf("stuntgres: invalid statistics snapshot signature")
	}
	return nil
}
func (n StatsSnapshot) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(StatsSnapshotRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statistics snapshot runtime")
	}
	return runtime.StatsSnapshotValue(n.Clear, n.Usage.Type)
}
func (n StatsSnapshot) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n StatsSnapshot) IntrinsicVolatility() expr.Volatility {
	if n.Clear {
		return expr.Volatile
	}
	return expr.Stable
}
func (StatsSnapshot) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (StatsSnapshot) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (StatsSnapshot) LooksLikeFunction() bool                 { return true }
func (StatsSnapshot) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (StatsSnapshot) SourceAdmission() (bool, bool)           { return true, false }
func (StatsSnapshot) Children() []expr.Expr                   { return nil }
func (StatsSnapshot) GroupLeaf()                              {}
func (n StatsSnapshot) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(StatsSnapshot)
	return ok && n.Clear == o.Clear
}
func (n StatsSnapshot) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid statistics snapshot children")
	}
	n.Info = info
	return n, nil
}
func (n StatsSnapshot) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	v := next.(StatsSnapshot)
	return v, v.Validate()
}
func (n StatsSnapshot) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	i := 0
	if n.Clear {
		i = 1
	}
	return expr.Rendering{Tree: "StatsSnapshot", Form: expr.FormCall, Name: statsSnapshotNames[i]}, nil
}
func (StatsSnapshot) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n StatsSnapshot) ValidateStoredProvider() error { return n.Validate() }
