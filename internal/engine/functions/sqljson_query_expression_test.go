package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type sqlJSONQueryUnitChild struct {
	temporalTestArgument
	label  string
	events *[]string
	err    error
}

func (e sqlJSONQueryUnitChild) Eval(expr.Context) (sqltype.Value, error) {
	*e.events = append(*e.events, e.label)
	return e.value, e.err
}

func TestSQLJSONQueryExpression(t *testing.T) {
	types := temporalTestTypes()
	ctx := &temporalTestContext{Context: context.Background()}
	input := func(id sqltype.ID, text string) sqltype.Value {
		t.Helper()
		v, err := types[id].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	events := []string(nil)
	child := func(label string, value sqltype.Value) sqlJSONQueryUnitChild {
		return sqlJSONQueryUnitChild{temporalTestArgument: temporalTestLiteral(value), label: label, events: &events}
	}
	raw := parser.SQLJSONQueryExpr{Kind: "json_value", Passing: []parser.SQLJSONPassing{{Name: parser.Identifier{Text: "x"}}}, Empty: parser.SQLJSONBehavior{Kind: "default", Default: parser.LiteralExpr{}}, Error: parser.SQLJSONBehavior{Kind: "default", Default: parser.LiteralExpr{}}}
	args := []expr.Expr{child("document", input(sqltype.JSONB, `{}`)), child("path", input(sqltype.JSONPath, `$x`)), child("passing", input(sqltype.Int4, "7")), child("empty", input(sqltype.Int4, "8")), child("error", input(sqltype.Int4, "9"))}
	info := expr.Info{Usage: types[sqltype.Int4].Usage()}
	node, err := NewSQLJSONQuery(info, raw, args, types[sqltype.JSONB].Usage())
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(ctx)
	if err != nil || !reflect.DeepEqual(events, []string{"document", "path", "passing"}) {
		t.Fatal(value, err, events)
	}
	rebuiltArgs := append([]expr.Expr(nil), args...)
	rebuiltArgs[1] = child("path", input(sqltype.JSONPath, `$.missing`))
	rebuilt, err := node.Rebuild(info, rebuiltArgs)
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	value, err = rebuilt.Eval(ctx)
	if err != nil || !reflect.DeepEqual(events, []string{"document", "path", "passing", "empty"}) {
		t.Fatal(value, err, events)
	}
	// The original plan and source children remain independent after reconstruction.
	events = nil
	_, err = node.Eval(ctx)
	if err != nil || len(events) != 3 {
		t.Fatal(err, events)
	}
	rebuiltArgs[0] = child("document", sqltype.Null(types[sqltype.JSONB]))
	rebuilt, err = node.Rebuild(info, rebuiltArgs)
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	value, err = rebuilt.Eval(ctx)
	if err != nil || !value.IsNull() || !reflect.DeepEqual(events, []string{"document"}) {
		t.Fatal(value, err, events)
	}
	ordinary := errors.New("ordinary child error")
	failing := child("passing", input(sqltype.Int4, "7"))
	failing.err = ordinary
	rebuiltArgs = append([]expr.Expr(nil), args...)
	rebuiltArgs[2] = failing
	rebuilt, err = node.Rebuild(info, rebuiltArgs)
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	_, err = rebuilt.Eval(ctx)
	if !errors.Is(err, ordinary) || !reflect.DeepEqual(events, []string{"document", "path", "passing"}) {
		t.Fatal(err, events)
	}
	if _, err = node.Rebuild(info, args[:4]); err == nil {
		t.Fatal("missing default child accepted")
	}
	if _, err = node.Rebuild(expr.Info{Usage: types[sqltype.Bool].Usage()}, append([]expr.Expr{args[2]}, args[1:]...)); err == nil {
		t.Fatal("foreign source plan accepted")
	}
	spec, err := node.Render()
	if err != nil || len(spec.Inputs) != 5 || spec.OwnedReturning != info.Usage {
		t.Fatal(spec, err)
	}
	t.Run("document input phase", func(t *testing.T) {
		document, err := newSQLJSONDocument(expr.Info{Usage: types[sqltype.JSONB].Usage()}, child("document", input(sqltype.Text, "x")))
		if err != nil {
			t.Fatal(err)
		}
		sourceArgs := append([]expr.Expr(nil), args...)
		sourceArgs[0] = document
		sourceArgs[1] = child("path", sqltype.Null(types[sqltype.JSONPath]))
		query, err := NewSQLJSONQuery(info, raw, sourceArgs, types[sqltype.JSONB].Usage())
		if err != nil {
			t.Fatal(err)
		}
		events = nil
		_, err = query.Eval(ctx)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "22P02" || !reflect.DeepEqual(events, []string{"document"}) {
			t.Fatal(err, events)
		}
		// Rebinding retains the input phase, without retaining the old input.
		replacement, err := document.Rebuild(expr.Info{Usage: types[sqltype.JSONB].Usage()}, []expr.Expr{child("replacement", input(sqltype.Text, "{}"))})
		if err != nil {
			t.Fatal(err)
		}
		sourceArgs[0] = replacement
		query, err = NewSQLJSONQuery(info, raw, sourceArgs, types[sqltype.JSONB].Usage())
		if err != nil {
			t.Fatal(err)
		}
		events = nil
		value, err := query.Eval(ctx)
		if err != nil || !value.IsNull() || !reflect.DeepEqual(events, []string{"replacement", "path"}) {
			t.Fatal(value, err, events)
		}
	})
}
