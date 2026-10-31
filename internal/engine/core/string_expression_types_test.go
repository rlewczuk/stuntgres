package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringCoreBinding(t *testing.T) {
	eng := engine.New()
	binder := enginetest.ExpressionBinder(t, context.Background(), eng, sqltype.InputEnvironment{}, nil)
	bind := func(query string) expr.Expr {
		t.Helper()
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
		node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		return node
	}
	first := bind("SELECT btrim(' x ')")
	if first.Properties().Volatility != expr.Immutable || first.ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal(first)
	}
	if !binder.SameGroupExpression(first, bind("SELECT btrim(' x ')")) || binder.SameGroupExpression(first, bind("SELECT ltrim(' x ')")) || binder.SameGroupExpression(first, bind("SELECT btrim(' y ')")) {
		t.Fatal("string provider grouping identity")
	}
	for _, tc := range []struct {
		query      string
		volatility expr.Volatility
	}{
		{"SELECT 'a'||'b'", expr.Immutable}, {"SELECT 'a'||42", expr.Stable}, {"SELECT 'a'||setseed(0)", expr.Volatile},
		{"SELECT setseed(0)||'a'", expr.Volatile}, {"SELECT normalize($1)", expr.Immutable},
	} {
		node := bind(tc.query)
		if node.Properties().Volatility != tc.volatility {
			t.Fatal(tc, node.Properties())
		}
		if tc.query == "SELECT normalize($1)" && !node.Properties().ParameterDependent {
			t.Fatal("lost parameter dependence")
		}
	}
	original := bind("SELECT btrim(pg_catalog.normalize('é','NFC'))")
	folded, changed, err := exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	if _, ok := original.(functions.String).Args[0].(functions.String); !ok {
		t.Fatal("preflight mutated analyzed children")
	}
	_, _, err = exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, bind("SELECT btrim(NULL,pg_catalog.normalize('x','bad'))"))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Position != "" {
		t.Fatal("immutable sibling before strict NULL", err)
	}
}

func TestStringCorePropertiesAndWalkers(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	stringPlan, err := sqltype.BindString("||", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), text.Usage()}, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Keep genuine owner retag/walker integration here; runtime demand is
	// exercised locally by functions.TestStringRuntimeDemand.
	node := functions.String{Info: expr.Info{Usage: text.Usage()}, Plan: stringPlan, Textual: [4]bool{true, true}, Args: []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "a")},
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "b")},
	}}
	var replacement *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			replacement = d
		}
	}
	rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == text {
			return replacement
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	got, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || got.Type() != replacement {
		t.Fatal("retained provider ownership", got, err)
	}
	s, _ := got.Text()
	if s != "ab" {
		t.Fatal(s)
	}
	if node.ResultUsage().Type != text || node.Plan.TextUsage().Type != text {
		t.Fatal("retag mutated original generation")
	}
	nested := node
	nested.Args = append([]expr.Expr(nil), node.Args...)
	nested.Args[1] = plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Pos: parser.Position{Character: 17}, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
	nested.Pos = parser.Position{Character: 8}
	nested.Property.RowDependent = true
	if position := analyze.FirstQueryBoundFrameVariable(nested, 1); position.Character != 17 {
		t.Fatal("nested string query-bound variable", position)
	}
}

func TestStringCoreMissingOperatorOwner(t *testing.T) {
	eng := engine.New()
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT 1 || 2", "operator does not exist: integer || integer", "10"},
	} {
		binder := enginetest.ExpressionBinder(t, context.Background(), eng, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, tc.query).(*parser.Select).Targets[0]
		_, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		if !errors.As(err, &got) || *got != want {
			t.Fatal("full missing operator diagnostic", got, err)
		}
	}
}
