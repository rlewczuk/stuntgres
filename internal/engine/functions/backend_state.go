package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BackendState retains only an INT4 slot recipe and ordinary routine provenance.
// Runtime authority and the activity cut are borrowed anew at execution.
type BackendState struct {
	expr.Info
	Kind     BackendStateKind
	Argument expr.Expr
}
type BackendStateKind uint8

const (
	BackendStateActivity BackendStateKind = iota
	BackendStateActivityStart
	BackendStateXactStart
	BackendStateWaitEvent
	BackendStateWaitEventType
	BackendStateSubxact
)

var backendStateNames = [...]string{"pg_stat_get_backend_activity", "pg_stat_get_backend_activity_start", "pg_stat_get_backend_xact_start", "pg_stat_get_backend_wait_event", "pg_stat_get_backend_wait_event_type", "pg_stat_get_backend_subxact"}
var backendStateTypes = [...]sqltype.ID{sqltype.Text, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Text, sqltype.Text, 2249}
var backendStateOIDs = [...]uint32{1940, 2094, 2857, 2853, 2788, 6107}

func (k BackendStateKind) Name() string {
	if int(k) >= len(backendStateNames) {
		return ""
	}
	return backendStateNames[k]
}
func (k BackendStateKind) ResultType() sqltype.ID {
	if int(k) >= len(backendStateTypes) {
		return 0
	}
	return backendStateTypes[k]
}

type BackendStateRuntime interface {
	BackendStateValue(BackendStateKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindBackendState(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := BackendStateKind(255)
	for i, candidate := range backendStateNames {
		if name == candidate {
			kind = BackendStateKind(i)
			break
		}
	}
	valid := len(raw.Args) == 1 && len(raw.ArgNames) == 0
	if kind == BackendStateSubxact {
		_, valid = namedSlots(raw, []string{"bid"}, 1)
	}
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	target := b.Type(sqltype.Int4).Usage()
	if operand.Usage.Type != nil {
		if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	argument, err := b.CoerceArgument(operand, CoercionRequest{Intent: ExactFormalArgument, Target: target})
	if err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.Type(kind.ResultType()).Usage()
	if kind == BackendStateSubxact {
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "subxact_count", Usage: b.Type(sqltype.Int4).Usage()},
			{Attribute: 2, Name: "subxact_overflowed", Usage: b.Type(sqltype.Bool).Usage()},
		})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, argument.Properties())), CatalogRoutineProvider, name, sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return BackendState{Info: info, Kind: kind, Argument: argument}, nil
}
func namedBackendState(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_stat_get_backend_subxact" || !declaredTypes(declared, []sqltype.ID{sqltype.Int4}) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, []string{"bid"}, 1)
}
func (n BackendState) Validate() error {
	if n.Kind.Name() == "" || n.Argument == nil || n.Usage.Type == nil || n.Usage.Type.ID() != n.Kind.ResultType() || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid backend state result")
	}
	u := n.Argument.ResultUsage()
	if u.Type == nil || u.Type.ID() != sqltype.Int4 || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid backend state operand")
	}
	if n.RoutineReference == nil || uint32(n.RoutineReference.RoutineOID()) != backendStateOIDs[n.Kind] {
		return fmt.Errorf("stuntgres: invalid backend state routine")
	}
	if n.Kind == BackendStateSubxact {
		if n.Usage.Type.CompositeLen() != 2 {
			return fmt.Errorf("stuntgres: invalid backend subxact shape")
		}
		for i, name := range []string{"subxact_count", "subxact_overflowed"} {
			m := n.Usage.Type.CompositeMember(i)
			id := sqltype.Int4
			if i == 1 {
				id = sqltype.Bool
			}
			if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != id || m.Usage != m.Usage.Type.Usage() {
				return fmt.Errorf("stuntgres: invalid backend subxact member")
			}
		}
	}
	return nil
}
func (n BackendState) Eval(ctx expr.Context) (sqltype.Value, error) {
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
	id, err := value.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(BackendStateRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing backend state runtime")
	}
	return runtime.BackendStateValue(n.Kind, id, n.Usage.Type)
}
func (n BackendState) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (BackendState) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (BackendState) PlanningFold() expr.Fold              { return expr.FoldNever }
func (BackendState) StrictNull() expr.Strict              { return expr.StrictAfterBare }
func (n BackendState) PreflightFold() expr.Preflight {
	if n.Kind == BackendStateSubxact {
		return expr.PreflightKeep
	}
	return expr.PreflightNullBare
}
func (BackendState) LooksLikeFunction() bool                 { return true }
func (BackendState) CallCost() int                           { return 1 }
func (BackendState) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (BackendState) SourceAdmission() (bool, bool)           { return true, false }
func (n BackendState) Children() []expr.Expr                 { return []expr.Expr{n.Argument} }
func (n BackendState) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(BackendState)
	return ok && n.Kind == o.Kind && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n BackendState) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(BackendState)
	return ok && n.Kind == o.Kind && same(n.Argument, o.Argument)
}
func (n BackendState) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid backend state reconstruction")
	}
	n.Info, n.Argument = info, children[0]
	return n, n.Validate()
}
func (n BackendState) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n BackendState) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "BackendState", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: []expr.RenderArg{{Expr: n.Argument}}}, nil
}
func (n BackendState) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: 1, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n BackendState) CaptureOperand(i int) (expr.Expr, error) {
	if i != 0 {
		return nil, fmt.Errorf("stuntgres: invalid backend state capture")
	}
	return n.Argument, nil
}
func (n BackendState) ValidateStoredProvider() error     { return n.Validate() }
func (BackendState) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.BackendState") }
