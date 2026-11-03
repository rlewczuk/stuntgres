package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BackendMemoryContexts retains only the output shape and routine provenance.
// Every invocation borrows the executing physical session, never Prepare's owner.
type BackendMemoryContexts struct {
	expr.Info
}

var backendMemoryNames = [...]string{"name", "ident", "type", "level", "path", "total_bytes", "total_nblocks", "free_bytes", "free_chunks", "used_bytes"}
var backendMemoryTypes = [...]sqltype.ID{25, 25, 25, 23, 1007, 20, 20, 20, 20, 20}

func bindBackendMemory(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) != 0 && len(raw.Args) <= 100 {
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
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pg_get_backend_memory_contexts(*) specified, but pg_get_backend_memory_contexts is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	members := make([]sqltype.CompositeMember, len(backendMemoryNames))
	for i, name := range backendMemoryNames {
		members[i] = sqltype.CompositeMember{Attribute: int32(i+1), Name: name, Usage: b.Type(backendMemoryTypes[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile}), CatalogRoutineProvider, "pg_get_backend_memory_contexts")
	if err != nil {
		return nil, err
	}
	return BackendMemoryContexts{info}, nil
}

func (n BackendMemoryContexts) Validate() error {
	d := n.Usage.Type
	if d == nil || !d.IsComposite() || d.ID() != 2249 || d.CompositeLen() != len(backendMemoryNames) {
		return fmt.Errorf("stuntgres: invalid backend memory record")
	}
	for i, name := range backendMemoryNames {
		member := d.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type == nil || member.Usage.Type.ID() != backendMemoryTypes[i] {
			return fmt.Errorf("stuntgres: invalid backend memory record member")
		}
	}
	return nil
}

func (BackendMemoryContexts) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: backend memory set used as scalar")
}

func (n BackendMemoryContexts) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, err := sessionInquiryRuntime(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := runtime.BackendMemoryContexts(n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}

func (BackendMemoryContexts) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (BackendMemoryContexts) PlanningFold() expr.Fold { return expr.FoldNever }
func (BackendMemoryContexts) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (BackendMemoryContexts) LooksLikeFunction() bool { return true }
func (BackendMemoryContexts) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (BackendMemoryContexts) SourceAdmission() (bool, bool) { return true, true }
func (BackendMemoryContexts) Children() []expr.Expr { return nil }
func (BackendMemoryContexts) GroupLeaf() {}
func (n BackendMemoryContexts) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(BackendMemoryContexts)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (BackendMemoryContexts) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(BackendMemoryContexts)
	return ok
}
func (n BackendMemoryContexts) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid backend memory arguments")
	}
	n.Info = info
	return n, nil
}
func (n BackendMemoryContexts) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(BackendMemoryContexts)
	return rebuilt, rebuilt.Validate()
}
func (n BackendMemoryContexts) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "BackendMemoryContexts", Form: expr.FormCall, Name: "pg_get_backend_memory_contexts"}, nil
}
func (BackendMemoryContexts) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n BackendMemoryContexts) ValidateStoredProvider() error { return n.Validate() }
func (BackendMemoryContexts) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.BackendMemoryContexts")
}
