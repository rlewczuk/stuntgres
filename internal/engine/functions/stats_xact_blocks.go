package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type StatsXactBlocksKind uint8

const (
	XactBlocksFetched StatsXactBlocksKind = iota
	XactBlocksHit
)

var statsXactBlocksNames = [...]string{"pg_stat_get_xact_blocks_fetched", "pg_stat_get_xact_blocks_hit"}

func (k StatsXactBlocksKind) Name() string {
	if int(k) >= len(statsXactBlocksNames) {
		return ""
	}
	return statsXactBlocksNames[k]
}

// StatsXactBlocks retains typed operands and routine provenance, never a writer
// or an evaluated pending value. Strict planning may erase a known NULL call.
type StatsXactBlocks struct {
	expr.Info
	Kind     StatsXactBlocksKind
	Argument expr.Expr
}

type StatsXactBlocksRuntime interface {
	StatsXactBlocksValue(StatsXactBlocksKind, uint32, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindStatsXactBlocks(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
		if len(raw.Args) == 1 {
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
	kind := StatsXactBlocksKind(255)
	for i, candidate := range statsXactBlocksNames {
		if name == candidate {
			kind = StatsXactBlocksKind(i)
			break
		}
	}
	if !visible || kind.Name() == "" || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Oid).Usage()
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if diagnostic, ok := failure.(*sqlstate.Error); ok && (diagnostic.Code == "42804" || diagnostic.Code == "42846") {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
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
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, arg.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Int8).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Oid)
	if err != nil {
		return nil, err
	}
	return StatsXactBlocks{Info: info, Kind: kind, Argument: arg}, nil
}

func (n StatsXactBlocks) Validate() error {
	if n.Kind.Name() == "" || n.Argument == nil || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Int8 || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid transaction block inquiry signature")
	}
	u := n.Argument.ResultUsage()
	if u.Type == nil || u.Type.ID() != sqltype.Oid || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid transaction block inquiry operand")
	}
	return nil
}
func (n StatsXactBlocks) Eval(ctx expr.Context) (sqltype.Value, error) {
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
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := value.OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(StatsXactBlocksRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing transaction block inquiry runtime")
	}
	return runtime.StatsXactBlocksValue(n.Kind, oid, n.Usage.Type)
}
func (n StatsXactBlocks) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (StatsXactBlocks) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (StatsXactBlocks) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (StatsXactBlocks) StrictNull() expr.Strict                 { return expr.StrictAfterBare }
func (StatsXactBlocks) PreflightFold() expr.Preflight           { return expr.PreflightNullBare }
func (StatsXactBlocks) LooksLikeFunction() bool                 { return true }
func (StatsXactBlocks) CallCost() int                           { return 1 }
func (StatsXactBlocks) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (StatsXactBlocks) SourceAdmission() (bool, bool)           { return true, false }
func (n StatsXactBlocks) Children() []expr.Expr                 { return []expr.Expr{n.Argument} }
func (n StatsXactBlocks) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(StatsXactBlocks)
	return ok && n.Kind == o.Kind && same(n.Argument, o.Argument)
}
func (n StatsXactBlocks) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid transaction block inquiry children")
	}
	n.Info, n.Argument = info, children[0]
	return n, n.Validate()
}
func (n StatsXactBlocks) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n StatsXactBlocks) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "StatsXactBlocks", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: []expr.RenderArg{{Expr: n.Argument}}}, nil
}
func (n StatsXactBlocks) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: 1, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n StatsXactBlocks) CaptureOperand(written int) (expr.Expr, error) {
	if written != 0 {
		return nil, fmt.Errorf("stuntgres: invalid transaction block inquiry capture")
	}
	return n.Argument, nil
}
func (n StatsXactBlocks) ValidateStoredProvider() error { return n.Validate() }
