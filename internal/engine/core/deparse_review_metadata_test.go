package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDeparseConstraintsReviewUnknownNullConsumerBoundary(t *testing.T) {
	e := engine.New()
	for _, expression := range []string{"NULL::unknown=1", "CAST(NULL AS pg_catalog.unknown)=1", "NULL::unknown::integer IS NULL", "NULL::unknown::pg_node_tree IS NULL"} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(expression, err)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(expression, err)
		}
		if node.ResultUsage().Type.ID() != sqltype.Bool {
			t.Fatal(node.ResultUsage())
		}
		if strings.HasSuffix(expression, "=1") {
			if !value.IsNull() {
				t.Fatal(expression, value)
			}
		} else {
			text, err := value.Type().Output(context.Background(), value)
			if err != nil || value.IsNull() || text != "t" {
				t.Fatal(expression, text, err)
			}
		}
	}
	for _, expression := range []string{"'x'::unknown", "$1::unknown"} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err == nil {
			node, err = b.FinalizeProjection(node)
		}
		if err != nil || node.ResultUsage().Type == nil || node.ResultUsage().Type.ID() != sqltype.Text {
			t.Fatal(expression, node, err)
		}
		if expression == "$1::unknown" && b.Params.Parameters[1].Type.ID() != sqltype.Text {
			t.Fatal("UNKNOWN parameter did not infer TEXT", b.Params.Parameters)
		}
	}
	for _, tc := range []struct{ expression, code, message, hint, position string }{
		{"1::unknown", "42846", "cannot cast type integer to unknown", "", "9"},
		{"unknown(1)", "42883", "function unknown(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
		{"CAST(NULL AS missing_schema.unknown)", "3F000", `schema "missing_schema" does not exist`, "", ""},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		_, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Hint != tc.hint || tc.position != "" && diagnostic.Position != tc.position {
			t.Fatal(tc.expression, err)
		}
	}
	_, err := parser.Parse("SELECT NULL::unknown(1) FROM t")
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "type modifiers are unsupported" || diagnostic.Position != "21" {
		t.Fatal(err)
	}
}

func TestDeparseConstraintsReviewConcreteNullCountArgument(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT num_nulls(NULL::integer) FROM t").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	leaf := node.(functions.NullCount).Args[0].(plan.LiteralExpr)
	if leaf.UnknownNull || leaf.Usage.Type.ID() != sqltype.Int4 || !leaf.Value.IsNull() {
		t.Fatal(leaf)
	}
	text, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), node, stored.DeparseParent{})
	if err != nil || text != "num_nulls(NULL::integer)" {
		t.Fatal(text, err)
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	count, err := value.Integer()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
