package enginetest

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Diagnostic requires err to be exactly want, with both severities ERROR.
func Diagnostic(t testing.TB, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

// FunctionCall builds an unqualified call of name at character 8.
func FunctionCall(name string, values ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: values, Pos: parser.Position{Character: 8}}
}

// BindSelect binds the SELECT q with the expression binder of op under its authorization.
func BindSelect(t testing.TB, e *core.Engine, op *session.Operation, q string) *plan.Query {
	t.Helper()
	raw := Statement(t, q).(*parser.Select)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	var query *plan.Query
	if err == nil {
		query, err = b.BindSelect(raw, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return query
}

// HostOwnerDefinition is a host language over hstore maps whose conversions, handler and
// increment program first run check.
func HostOwnerDefinition(check func() error) host.LanguageDefinition {
	enter := func() error {
		if check != nil {
			return check()
		}
		return nil
	}
	from := func(_ context.Context, v host.SQLValue) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		if v.Null {
			return host.Object{Null: true}, nil
		}
		m := make(host.HstoreMap, len(v.Hstore))
		for _, p := range v.Hstore {
			m[p.Key] = host.HstoreValue{Text: p.Value, Null: p.Null}
		}
		return host.Object{Value: m}, nil
	}
	to := func(_ context.Context, v host.Object) (host.SQLValue, error) {
		if err := enter(); err != nil {
			return host.SQLValue{}, err
		}
		if v.Null {
			return host.SQLValue{Type: host.Hstore, Null: true}, nil
		}
		m, ok := v.Value.(host.HstoreMap)
		if !ok || m == nil {
			return host.SQLValue{}, errors.New("wrong map")
		}
		entries := make([]host.HstoreEntry, 0, len(m))
		for k, v := range m {
			entries = append(entries, host.HstoreEntry{Key: k, Value: v.Text, Null: v.Null})
		}
		return host.SQLValue{Type: host.Hstore, Hstore: entries}, nil
	}
	program := func(_ context.Context, args []host.Object) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		m, ok := args[0].Value.(host.HstoreMap)
		if !ok {
			return host.Object{}, errors.New("wrong input")
		}
		n, err := strconv.Atoi(m["a"].Text)
		if err != nil {
			return host.Object{}, err
		}
		m["a"] = host.HstoreValue{Text: strconv.Itoa(n + 1)}
		m["c"] = host.HstoreValue{Text: "3"}
		return host.Object{Value: m}, nil
	}
	return host.LanguageDefinition{Provider: "owner-host", ABIVersion: "map-v1", Name: "owner_host", HandlerName: host.Name{Schema: "public", Name: "owner_host_handler"}, Handler: func(ctx context.Context, p host.ProgramFunc, args []host.Object) (host.Object, error) {
		if err := enter(); err != nil {
			return host.Object{}, err
		}
		return p(ctx, args)
	}, Conversions: []host.ConversionDefinition{{ID: "map", Type: host.Hstore, Bootstrap: true, FromName: host.Name{Schema: "public", Name: "owner_host_from"}, ToName: host.Name{Schema: "public", Name: "owner_host_to"}, FromSQL: from, ToSQL: to}}, Programs: []host.ProgramDefinition{{ID: "increment", Version: "1", Inputs: []host.Type{host.Hstore}, Result: host.Hstore, Execute: program}}}
}

// QueryProbeExpression is an expression whose evaluation calls Probe with the engine context.
type QueryProbeExpression struct {
	expr.Info
	Probe func(*exec.Context) (sqltype.Value, error)
}

func (e QueryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.Probe(EvalContext(ctx))
}

// QueryProbe returns a probe expression of usage evaluated by eval.
func QueryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return QueryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, Probe: eval}
}

// TypedLiteral is the string literal value cast to the type named typ.
func TypedLiteral(typ, value string) parser.Expr {
	return parser.CastExpr{Child: StringLiteral(value), TypeName: parser.TypeName{Name: typ}}
}

// StringLiteral is an untyped string literal.
func StringLiteral(s string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
}

// EvalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func EvalContext(ctx expr.Context) *exec.Context {
	c, _ := ctx.(*exec.Context)
	return c
}
