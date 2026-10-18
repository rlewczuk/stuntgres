package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func sequenceInputExecute(t *testing.T, e *Engine, op *Operation, query string, args ...any) (Result, error) {
	t.Helper()
	ast := statement(t, query)
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal("parameter analysis must not resolve runtime names", err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &description
	r, err := scope.Execute(ast, len(args), func(ctx context.Context, info ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		for i, arg := range args {
			if arg == nil {
				frame[i] = sqltype.Null(info.Types[i].Type)
				continue
			}
			value, err := info.Types[i].Type.InputWithEnvironment(ctx, fmt.Sprint(arg), -1, input)
			if err != nil {
				return sequenceInputError(err, parser.Position{}, "", i+1)
			}
			frame[i] = value
		}
		return nil
	})
	return r, scope.Finish(err)
}

func TestSequenceRegclassParameterCanonicalDatum(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE SEQUENCE s")
	ref := roleTableRef(t, e, "s")
	for _, tc := range []struct{ input, canonical, output string }{{"s", fmt.Sprint(ref.Address.ObjectID), "s"}, {"4294967295", "4294967295", "4294967295"}, {"-", "0", "-"}, {"0", "0", "-"}} {
		r, err := sequenceInputExecute(t, e, op, "SELECT $1::regclass", tc.input)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		frame := make([]sqltype.Value, 1)
		present, err := r.Rows.Next(context.Background(), frame)
		if err != nil || !present {
			r.Rows.Close()
			t.Fatal(present, err)
		}
		if frame[0].Type().ID() != sqltype.Regclass {
			r.Rows.Close()
			t.Fatal("datum lost regclass type", frame[0])
		}
		if text, err := frame[0].Text(); err != nil || text != tc.canonical {
			r.Rows.Close()
			t.Fatal("noncanonical datum", text, tc.canonical, err)
		}
		if text, err := r.Rows.RegclassText(frame[0]); err != nil || text != tc.output {
			r.Rows.Close()
			t.Fatal(text, tc.output, err)
		}
		r.Rows.Close()
	}
}

func TestSequenceRegclassParameterNamespaceInput(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER seq_input_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s"} {
		roleExecute(t, e, op, q)
	}
	ref := roleTableRef(t, e, "private.s")
	namespace := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: e.state.descriptors[ref.Address].Namespace()}
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_input_reader")
	for _, tc := range []struct {
		query string
		args  []any
	}{{"SELECT $1::regclass", []any{"private.s"}}, {"SELECT setval($1,$2)", []any{"private.s", nil}}} {
		r, err := sequenceInputExecute(t, e, op, tc.query, tc.args...)
		if r.Rows != nil {
			r.Rows.Close()
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private", Context: "unnamed portal parameter $1 = '...'"}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: got=%#v want=%#v", tc.query, got, want)
		}
	}
	// Numeric input bypasses name/schema resolution; projection does not open
	// the relation or require its sequence ACL.
	r, err := sequenceInputExecute(t, e, op, "SELECT $1::regclass", fmt.Sprint(ref.Address.ObjectID))
	if err != nil {
		t.Fatal("numeric input required source namespace access", err)
	}
	r.Rows.Close()
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	r = roleExecute(t, e, op, "SELECT nextval('private.s')")
	if got := tuples(r); got[0][0] != int64(1) {
		t.Fatal("input denial consumed sequence", got)
	}
	r.Rows.Close()
}
