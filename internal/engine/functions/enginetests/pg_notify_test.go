package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGNotifyBindingDiagnostics(t *testing.T) {
	ctx, e := context.Background(), engine.New()
	const position = "8"
	cases := []struct{ name, query, message string }{
		{"integer", "SELECT pg_notify('chan', 1)", "function pg_notify(unknown, integer) does not exist"},
		{"numeric", "SELECT pg_notify('chan', 1.0)", "function pg_notify(unknown, numeric) does not exist"},
		{"named-forward", "SELECT pg_notify(channel => 'chan', payload => 'p')", "function pg_notify(channel => unknown, payload => unknown) does not exist"},
		{"named-reverse", "SELECT pg_notify(payload => 'p', channel => 'chan')", "function pg_notify(payload => unknown, channel => unknown) does not exist"},
		{"no-arguments", "SELECT pg_notify()", "function pg_notify() does not exist"},
		{"star", "SELECT pg_notify(*)", "function pg_notify() does not exist"},
		{"one-argument", "SELECT pg_notify('chan')", "function pg_notify(unknown) does not exist"},
		{"three-arguments", "SELECT pg_notify('chan','p','x')", "function pg_notify(unknown, unknown, unknown) does not exist"},
		{"quoted-name", `SELECT "PG_NOTIFY"('chan','p')`, "function PG_NOTIFY(unknown, unknown) does not exist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			target := enginetest.Statement(t, c.query).(*parser.Select).Targets[0]
			node, err := b.Bind(target, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			want := &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR",
				Code: "42883", Message: c.message,
				Hint: functions.SignatureHint, Position: position,
			}
			diagnostic, ok := err.(*sqlstate.Error)
			if node != nil || !ok || *diagnostic != *want {
				t.Fatal("missing signature", node, err)
			}
		})
	}

	for _, query := range []string{
		"SELECT pg_notify('chan','payload')",
		"SELECT pg_notify('c'::varchar,'p'::varchar)",
		"SELECT pg_notify(NULL, 'p')",
		"SELECT pg_notify('chan', NULL)",
	} {
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		target := enginetest.Statement(t, query).(*parser.Select).Targets[0]
		node, err := b.Bind(target, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		call, ok := node.(functions.PgNotify)
		if err != nil || !ok || node.ResultUsage().Type.ID() != sqltype.Void || call.Args[0].ResultUsage().Type.ID() != sqltype.Text || call.Args[1].ResultUsage().Type.ID() != sqltype.Text || node.Properties().Volatility != expr.Volatile {
			t.Fatal("bound signature", query, node, err)
		}
	}

	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	target := enginetest.Statement(t, "SELECT pg_notify($1, $2)").(*parser.Select).Targets[0]
	node, err := b.Bind(target, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	first, firstOK := b.Params.Parameters[1]
	second, secondOK := b.Params.Parameters[2]
	if err != nil || node.ResultUsage().Type.ID() != sqltype.Void || len(b.Params.Parameters) != 2 || !firstOK || !secondOK || first.Type == nil || first.Type.ID() != sqltype.Text || second.Type == nil || second.Type.ID() != sqltype.Text {
		t.Fatal("untyped parameter inference", node, b.Params.Parameters, err)
	}

	defaults := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	defaults.Consumer.DefaultConsumer = true
	target = enginetest.Statement(t, "SELECT pg_notify('chan','p')").(*parser.Select).Targets[0]
	node, err = defaults.Bind(target, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	want := &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "0A000", Message: "functions are unsupported", Position: position,
	}
	diagnostic, ok := err.(*sqlstate.Error)
	if node != nil || !ok || *diagnostic != *want {
		t.Fatal("column DEFAULT boundary", node, err)
	}
}
