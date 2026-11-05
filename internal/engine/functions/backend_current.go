package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BackendCurrent retains only a typed PID recipe, fixed OUT shape and ordinary
// routine provenance. In particular Prepare never borrows monitoring state.
type BackendCurrent struct {
	expr.Info
	Kind     BackendCurrentKind
	Argument expr.Expr
}
type BackendCurrentKind uint8

const (
	BackendActivity BackendCurrentKind = iota
	BackendIO
	BackendWAL
)

var backendCurrentNames = [...]string{"pg_stat_get_activity", "pg_stat_get_backend_io", "pg_stat_get_backend_wal"}
var backendCurrentFields = [...][]string{
	{"datid", "pid", "usesysid", "application_name", "state", "query", "wait_event_type", "wait_event", "xact_start", "query_start", "backend_start", "state_change", "client_addr", "client_hostname", "client_port", "backend_xid", "backend_xmin", "backend_type", "ssl", "sslversion", "sslcipher", "sslbits", "ssl_client_dn", "ssl_client_serial", "ssl_issuer_dn", "gss_auth", "gss_princ", "gss_enc", "gss_delegation", "leader_pid", "query_id"},
	{"backend_type", "object", "context", "reads", "read_bytes", "read_time", "writes", "write_bytes", "write_time", "writebacks", "writeback_time", "extends", "extend_bytes", "extend_time", "hits", "evictions", "reuses", "fsyncs", "fsync_time", "stats_reset"},
	{"wal_records", "wal_fpi", "wal_bytes", "wal_buffers_full", "stats_reset"},
}
var backendCurrentTypes = [...][]sqltype.ID{
	{26, 23, 26, 25, 25, 25, 25, 25, 1184, 1184, 1184, 1184, 869, 25, 23, 28, 28, 25, 16, 25, 25, 23, 25, 1700, 25, 16, 25, 16, 16, 23, 20},
	{25, 25, 25, 20, 1700, 701, 20, 1700, 701, 20, 701, 20, 1700, 701, 20, 20, 20, 20, 701, 1184},
	{20, 20, 1700, 20, 1184},
}

func (k BackendCurrentKind) Name() string {
	if int(k) >= len(backendCurrentNames) {
		return ""
	}
	return backendCurrentNames[k]
}
func (k BackendCurrentKind) inputName() string {
	if k == BackendActivity {
		return "pid"
	}
	return "backend_pid"
}
func (k BackendCurrentKind) volatility() expr.Volatility {
	if k == BackendActivity {
		return expr.Stable
	}
	return expr.Volatile
}

// The three operations preserve SRF versus scalar cardinality in their types.
type BackendCurrentRuntime interface {
	BackendActivity(sqltype.Value, *sqltype.Descriptor) ([]sqltype.Value, error)
	BackendIO(int64, *sqltype.Descriptor) ([]sqltype.Value, error)
	BackendWAL(int64, *sqltype.Descriptor) (sqltype.Value, error)
}

func bindBackendCurrent(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	names := make([]string, min(len(raw.Args), 100))
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
	kind := BackendCurrentKind(len(backendCurrentNames))
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	for i, n := range backendCurrentNames {
		if n == name {
			kind = BackendCurrentKind(i)
			break
		}
	}
	_, valid := namedSlots(raw, []string{kind.inputName()}, 1)
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
	if kind != BackendWAL {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
	}
	members := make([]sqltype.CompositeMember, len(backendCurrentFields[kind]))
	for i, field := range backendCurrentFields[kind] {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: field, Usage: b.Type(backendCurrentTypes[kind][i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.MergeProperties(expr.Properties{Volatility: kind.volatility()}, argument.Properties())), CatalogRoutineProvider, name, sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return BackendCurrent{Info: info, Kind: kind, Argument: argument}, nil
}
func namedBackendCurrent(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if !declaredTypes(declared, []sqltype.ID{sqltype.Int4}) {
		return nil, nil, false
	}
	label := "backend_pid"
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "pg_stat_get_activity" {
		label = "pid"
	}
	return fixedNamedInputs(b, raw, args, declared, []string{label}, 1)
}
func (n BackendCurrent) Validate() error {
	if n.Kind.Name() == "" || n.Argument == nil || n.Argument.ResultUsage().Type == nil || n.Argument.ResultUsage().Type.ID() != sqltype.Int4 || n.Usage.Type == nil || n.Usage.Type.ID() != 2249 || n.Usage.Type.CompositeLen() != len(backendCurrentFields[n.Kind]) {
		return fmt.Errorf("stuntgres: invalid backend inquiry signature")
	}
	for i, name := range backendCurrentFields[n.Kind] {
		m := n.Usage.Type.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != backendCurrentTypes[n.Kind][i] {
			return fmt.Errorf("stuntgres: invalid backend inquiry OUT member")
		}
	}
	return nil
}
func (n BackendCurrent) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind != BackendWAL {
		return sqltype.Value{}, fmt.Errorf("stuntgres: backend inquiry set used as scalar")
	}
	value, err := n.Argument.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, ok := ctx.(BackendCurrentRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing backend inquiry runtime")
	}
	pid, err := value.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.BackendWAL(pid, n.Usage.Type)
}
func (n BackendCurrent) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if n.Kind == BackendWAL {
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
	value, err := n.Argument.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if n.Kind == BackendIO && value.IsNull() {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	runtime, ok := ctx.(BackendCurrentRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing backend inquiry runtime")
	}
	var values []sqltype.Value
	if n.Kind == BackendActivity {
		values, err = runtime.BackendActivity(value, n.Usage.Type)
	} else {
		pid, failure := value.Integer()
		if failure != nil {
			return expr.SourceCursor{}, failure
		}
		values, err = runtime.BackendIO(pid, n.Usage.Type)
	}
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}
func (n BackendCurrent) IntrinsicVolatility() expr.Volatility  { return n.Kind.volatility() }
func (BackendCurrent) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (BackendCurrent) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (BackendCurrent) LooksLikeFunction() bool                 { return true }
func (BackendCurrent) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n BackendCurrent) SourceAdmission() (bool, bool)         { return true, n.Kind != BackendWAL }
func (n BackendCurrent) Children() []expr.Expr                 { return []expr.Expr{n.Argument} }
func (n BackendCurrent) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(BackendCurrent)
	return ok && n.Kind == o.Kind && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n BackendCurrent) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(BackendCurrent)
	return ok && n.Kind == o.Kind && same(n.Argument, o.Argument)
}
func (n BackendCurrent) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid backend inquiry reconstruction")
	}
	n.Info, n.Argument = info, children[0]
	return n, nil
}
func (n BackendCurrent) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	v, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := v.(BackendCurrent)
	return rebuilt, rebuilt.Validate()
}
func (n BackendCurrent) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "BackendCurrent", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: []expr.RenderArg{{Expr: n.Argument}}}, nil
}
func (BackendCurrent) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n BackendCurrent) CaptureOperand(i int) (expr.Expr, error) {
	if i != 0 {
		return nil, fmt.Errorf("stuntgres: invalid backend inquiry capture")
	}
	return n.Argument, nil
}
func (n BackendCurrent) ValidateStoredProvider() error     { return n.Validate() }
func (BackendCurrent) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.BackendCurrent") }
