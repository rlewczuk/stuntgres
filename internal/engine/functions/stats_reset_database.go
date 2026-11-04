package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type StatsResetDatabaseKind uint8

const (
	ResetDatabase StatsResetDatabaseKind = iota
	ResetTableCounters
	ResetFunctionCounters
)

var statsResetDatabaseNames = [...]string{"pg_stat_reset", "pg_stat_reset_single_table_counters", "pg_stat_reset_single_function_counters"}

func (k StatsResetDatabaseKind) Name() string {
	if int(k) >= len(statsResetDatabaseNames) {
		return ""
	}
	return statsResetDatabaseNames[k]
}

type StatsResetDatabase struct {
	expr.Info
	Kind     StatsResetDatabaseKind
	Argument expr.Expr
}

type StatsResetDatabaseRuntime interface {
	StatsResetDatabase(StatsResetDatabaseKind, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindStatsResetDatabase(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := StatsResetDatabaseKind(len(statsResetDatabaseNames))
	for i, n := range statsResetDatabaseNames {
		if name == n {
			kind = StatsResetDatabaseKind(i)
			break
		}
	}
	var labels []string
	if kind != ResetDatabase {
		labels = []string{""}
	}
	_, valid := namedSlots(raw, labels, len(labels))
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var argument expr.Expr
	properties := expr.Properties{Volatility: expr.Volatile}
	var formals []sqltype.ID
	if kind != ResetDatabase {
		target := b.Type(sqltype.Oid).Usage()
		if operand.Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
		argument, err = b.CoerceArgument(operand, CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, argument.Properties())
		formals = []sqltype.ID{sqltype.Oid}
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals...)
	if err != nil {
		return nil, err
	}
	return StatsResetDatabase{Info: info, Kind: kind, Argument: argument}, nil
}

func (n StatsResetDatabase) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Void {
		return fmt.Errorf("stuntgres: invalid database statistics reset signature")
	}
	if n.Kind == ResetDatabase {
		if n.Argument != nil {
			return fmt.Errorf("stuntgres: unexpected database statistics reset operand")
		}
	} else if n.Argument == nil || n.Argument.ResultUsage().Type == nil || n.Argument.ResultUsage().Type.ID() != sqltype.Oid {
		return fmt.Errorf("stuntgres: invalid object statistics reset operand")
	}
	return nil
}
func (n StatsResetDatabase) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var value sqltype.Value
	if n.Argument != nil {
		var err error
		value, err = n.Argument.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
	}
	runtime, ok := ctx.(StatsResetDatabaseRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing database statistics reset runtime")
	}
	return runtime.StatsResetDatabase(n.Kind, value, n.Usage.Type)
}
func (StatsResetDatabase) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (StatsResetDatabase) PlanningFold() expr.Fold              { return expr.FoldNever }
func (n StatsResetDatabase) StrictNull() expr.Strict {
	if n.Kind == ResetDatabase {
		return expr.StrictNone
	}
	return expr.StrictAfter
}
func (StatsResetDatabase) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (StatsResetDatabase) LooksLikeFunction() bool                 { return true }
func (StatsResetDatabase) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (StatsResetDatabase) SourceAdmission() (bool, bool)           { return true, false }
func (n StatsResetDatabase) Children() []expr.Expr {
	if n.Argument == nil {
		return nil
	}
	return []expr.Expr{n.Argument}
}
func (n StatsResetDatabase) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n StatsResetDatabase) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(StatsResetDatabase)
	return ok && n.Kind == o.Kind && n.ResultUsage() == o.ResultUsage()
}
func (n StatsResetDatabase) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(StatsResetDatabase)
	return ok && n.Kind == o.Kind && (n.Argument == nil && o.Argument == nil || n.Argument != nil && o.Argument != nil && same(n.Argument, o.Argument))
}
func (n StatsResetDatabase) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 0
	if n.Kind != ResetDatabase {
		count = 1
	}
	if len(children) != count || count == 1 && children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid database statistics reset reconstruction")
	}
	n.Info = info
	if count == 1 {
		n.Argument = children[0]
	}
	return n, nil
}
func (n StatsResetDatabase) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	v, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := v.(StatsResetDatabase)
	return rebuilt, rebuilt.Validate()
}
func (n StatsResetDatabase) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	var args []expr.RenderArg
	if n.Argument != nil {
		args = []expr.RenderArg{{Expr: n.Argument}}
	}
	return expr.Rendering{Tree: "StatsResetDatabase", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n StatsResetDatabase) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	count := 0
	if n.Argument != nil {
		count = 1
	}
	return expr.CaptureOperands{Count: count, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n StatsResetDatabase) CaptureOperand(i int) (expr.Expr, error) {
	if i != 0 || n.Argument == nil {
		return nil, fmt.Errorf("stuntgres: invalid database statistics reset capture")
	}
	return n.Argument, nil
}
func (n StatsResetDatabase) ValidateStoredProvider() error { return n.Validate() }
func (StatsResetDatabase) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.StatsResetDatabase")
}
