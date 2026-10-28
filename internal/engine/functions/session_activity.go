package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SessionActivityKind identifies a live inquiry, never its evaluated value.
type SessionActivityKind uint8

const (
	ActivityQuery SessionActivityKind = iota
	ActivityBackendPID
	ActivityPostmasterStart
	ActivityConfigLoad
)

var sessionActivityNames = [...]string{"current_query", "pg_backend_pid", "pg_postmaster_start_time", "pg_conf_load_time"}

// SessionActivity retains only the immutable typed recipe and routine provenance.
type SessionActivity struct {
	expr.Info
	Kind SessionActivityKind
}

func bindSessionActivity(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil { name = b.DiagnosticTypeName(arg.Usage.Type) }
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil, err }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := SessionActivityKind(len(sessionActivityNames))
	for i, candidate := range sessionActivityNames { if candidate == name { kind = SessionActivityKind(i); break } }
	if !visible || int(kind) >= len(sessionActivityNames) || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	id := sqltype.Timestamptz
	if kind == ActivityQuery { id = sqltype.Text } else if kind == ActivityBackendPID { id = sqltype.Int4 }
	volatility := expr.Stable
	if kind == ActivityQuery { volatility = expr.Volatile }
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(id).Usage(), raw.Pos, expr.Properties{Volatility: volatility}), CatalogRoutineProvider, name)
	if err != nil { return nil, err }
	return SessionActivity{info, kind}, nil
}

func (n SessionActivity) Validate() error {
	if int(n.Kind) >= len(sessionActivityNames) || n.Usage.Type == nil { return fmt.Errorf("stuntgres: invalid session activity signature") }
	id := sqltype.Timestamptz
	if n.Kind == ActivityQuery { id = sqltype.Text } else if n.Kind == ActivityBackendPID { id = sqltype.Int4 }
	if n.Usage.Type.ID() != id { return fmt.Errorf("stuntgres: invalid session activity result type") }
	return nil
}

func (n SessionActivity) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	runtime, ok := ctx.(SessionActivityRuntime)
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing session activity runtime") }
	return runtime.SessionActivityValue(n.Kind, n.Usage.Type)
}

func (n SessionActivity) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
