package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BackendIdentity contains only the ordinary routine identity and typed slot
// recipe. Neither binding nor reconstruction captures a provider or authority.
type BackendIdentity struct {
	expr.Info
	Kind     BackendIdentityKind
	Argument expr.Expr // nil only for the zero-input ID set
}

type BackendIdentityKind uint8

const (
	BackendIDSet BackendIdentityKind = iota
	BackendPID
	BackendDatabaseID
	BackendUserID
	BackendStart
	BackendClientAddress
	BackendClientPort
)

var backendIdentityNames = [...]string{"pg_stat_get_backend_idset", "pg_stat_get_backend_pid", "pg_stat_get_backend_dbid", "pg_stat_get_backend_userid", "pg_stat_get_backend_start", "pg_stat_get_backend_client_addr", "pg_stat_get_backend_client_port"}
var backendIdentityTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Oid, sqltype.Oid, sqltype.Timestamptz, sqltype.Inet, sqltype.Int4}

func (k BackendIdentityKind) Name() string {
	if int(k) >= len(backendIdentityNames) {
		return ""
	}
	return backendIdentityNames[k]
}
func (k BackendIdentityKind) ResultType() sqltype.ID {
	if int(k) >= len(backendIdentityTypes) {
		return 0
	}
	return backendIdentityTypes[k]
}

type BackendIdentityRuntime interface {
	BackendIdentityIDs(*sqltype.Descriptor) ([]sqltype.Value, error)
	BackendIdentityValue(BackendIdentityKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindBackendIdentity(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	kind := BackendIdentityKind(255)
	for i, candidate := range backendIdentityNames {
		if candidate == name {
			kind = BackendIdentityKind(i)
			break
		}
	}
	count := 1
	if kind == BackendIDSet {
		count = 0
	}
	if !visible || kind.Name() == "" || len(raw.Args) != count || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var argument expr.Expr
	properties := expr.Properties{Volatility: expr.Stable}
	var input []sqltype.ID
	if count == 1 {
		target := b.Type(sqltype.Int4).Usage()
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
		input = []sqltype.ID{sqltype.Int4}
	}
	// Ordinary signature resolution precedes aggregate-star classification.
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if kind == BackendIDSet {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(kind.ResultType()).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, input...)
	if err != nil {
		return nil, err
	}
	return BackendIdentity{Info: info, Kind: kind, Argument: argument}, nil
}

func (n BackendIdentity) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != n.Kind.ResultType() || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid backend identity result")
	}
	if n.Kind == BackendIDSet {
		if n.Argument != nil {
			return fmt.Errorf("stuntgres: backend ID set has an operand")
		}
	} else {
		if n.Argument == nil {
			return fmt.Errorf("stuntgres: missing backend identity operand")
		}
		u := n.Argument.ResultUsage()
		if u.Type == nil || u.Type.ID() != sqltype.Int4 || u != u.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid backend identity operand")
		}
	}
	return nil
}
func (n BackendIdentity) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == BackendIDSet {
		return sqltype.Value{}, fmt.Errorf("stuntgres: backend ID set used as scalar")
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
	runtime, ok := ctx.(BackendIdentityRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing backend identity runtime")
	}
	return runtime.BackendIdentityValue(n.Kind, id, n.Usage.Type)
}
func (n BackendIdentity) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if n.Kind != BackendIDSet {
		value, err := n.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, ok := ctx.(BackendIdentityRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing backend identity runtime")
	}
	values, err := runtime.BackendIdentityIDs(n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}
func (BackendIdentity) IntrinsicVolatility() expr.Volatility    { return expr.Stable }
func (BackendIdentity) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (BackendIdentity) StrictNull() expr.Strict                 { return expr.StrictAfterBare }
func (BackendIdentity) PreflightFold() expr.Preflight           { return expr.PreflightNullBare }
func (BackendIdentity) LooksLikeFunction() bool                 { return true }
func (BackendIdentity) CallCost() int                           { return 1 }
func (BackendIdentity) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n BackendIdentity) SourceAdmission() (bool, bool)         { return true, n.Kind == BackendIDSet }
func (n BackendIdentity) Children() []expr.Expr {
	if n.Kind == BackendIDSet {
		return nil
	}
	return []expr.Expr{n.Argument}
}
func (n BackendIdentity) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(BackendIdentity)
	return ok && n.Kind == o.Kind && n.ResultUsage() == o.ResultUsage()
}
func (n BackendIdentity) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(BackendIdentity)
	return ok && n.Kind == o.Kind && (n.Kind == BackendIDSet || same(n.Argument, o.Argument))
}
func (n BackendIdentity) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 1
	if n.Kind == BackendIDSet {
		count = 0
	}
	if len(children) != count {
		return nil, fmt.Errorf("stuntgres: invalid backend identity reconstruction")
	}
	n.Info = info
	if count != 0 {
		n.Argument = children[0]
	}
	return n, n.Validate()
}
func (n BackendIdentity) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n BackendIdentity) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	r := expr.Rendering{Tree: "BackendIdentity", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name()}
	if n.Argument != nil {
		r.Args = []expr.RenderArg{{Expr: n.Argument}}
	}
	return r, nil
}
func (n BackendIdentity) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	count := 1
	if n.Kind == BackendIDSet {
		count = 0
	}
	return expr.CaptureOperands{Count: count, Indexed: count != 0}, kind == expr.CaptureFunction, nil
}
func (n BackendIdentity) CaptureOperand(i int) (expr.Expr, error) {
	if n.Kind == BackendIDSet || i != 0 {
		return nil, fmt.Errorf("stuntgres: invalid backend identity capture")
	}
	return n.Argument, nil
}
func (n BackendIdentity) ValidateStoredProvider() error { return n.Validate() }
func (BackendIdentity) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.BackendIdentity")
}
