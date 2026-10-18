package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringEncodingFormalBinding(t *testing.T) {
	for _, tc := range []struct {
		query          string
		result, formal sqltype.ID
		parameter      int
	}{
		{"SELECT chr($1)", sqltype.Text, sqltype.Int4, 1},
		{"SELECT to_ascii('é',$1)", sqltype.Text, sqltype.Name, 1},
		{"SELECT to_ascii('é',$1::int)", sqltype.Text, sqltype.Int4, 1},
		{"SELECT to_ascii('é',8::smallint)", sqltype.Text, sqltype.Int4, 0},
		{"SELECT to_ascii('é','LATIN1'::text)", sqltype.Text, sqltype.Name, 0},
		{"SELECT ascii($1)", sqltype.Int4, sqltype.Text, 1},
		{"SELECT unicode_assigned($1)", sqltype.Bool, sqltype.Text, 1},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := statement(t, tc.query).(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		n, ok := node.(functions.String)
		if !ok {
			t.Fatalf("wrong node %T", node)
		}
		slot := 0
		if n.Plan.Name() == "to_ascii" {
			slot = 1
		}
		if n.Plan.ArgumentUsage(slot).Type.ID() != tc.formal || n.ResultUsage().Type.ID() != tc.result || n.Properties().Volatility != expr.Immutable || n.Plan.SessionDependent() {
			t.Fatal("formal role/properties", tc, n)
		}
		if tc.parameter != 0 && (b.parameters[tc.parameter].Type.ID() != tc.formal || !n.Properties().ParameterDependent) {
			t.Fatal("parameter source contract", tc, b.parameters)
		}
		if slot == 1 && n.Args[slot].ResultUsage().Type.ID() != tc.formal {
			t.Fatal("formal conversion was not bound", tc, n.Args[slot])
		}
	}
	for _, query := range []string{"SELECT chr(65::bigint)", "SELECT chr('65'::text)", "SELECT to_ascii('é',true)"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42883" {
			t.Fatal("wrong source admitted", query, err)
		}
	}
}

func TestStringEncodingRetainedFormalOwnership(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, query := range []string{"SELECT to_ascii('é','LATIN1'::text)", "SELECT to_ascii('é',8::smallint)", "SELECT chr(65)", "SELECT ascii('é')", "SELECT unicode_assigned('é')", "SELECT unistr('plain')"} {
		node, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		original := node.(functions.String)
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal("retag", query, changed, err)
		}
		retained := rebound.(functions.String)
		if retained.ResultUsage().Type != replacements[original.ResultUsage().Type.ID()] || retained.Plan.TextUsage().Type != replacements[sqltype.Text] {
			t.Fatal("result/text owner", query)
		}
		for i := range original.Args {
			id := original.Plan.ArgumentUsage(i).Type.ID()
			if retained.Plan.ArgumentUsage(i).Type != replacements[id] || original.Plan.ArgumentUsage(i).Type == retained.Plan.ArgumentUsage(i).Type {
				t.Fatal("formal ownership", query, i)
			}
		}
		value, err := retained.Eval(&EvalContext{Context: context.Background()})
		if err != nil || value.Type() != retained.ResultUsage().Type {
			t.Fatal("retained evaluation", query, value, err)
		}
	}
}

func TestStringEncodingChildrenAndStructuralIdentity(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, name := b.typ(sqltype.Text), b.typ(sqltype.Name)
	args := []expr.Expr{
		literalExpression{expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable, ParameterDependent: true}}, sqltype.Null(text)},
		literalExpression{expr.Info{Usage: name.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, sqltype.String(name, "nope")},
	}
	node, err := b.stringNode("to_ascii", text.Usage(), parser.Position{}, args, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	// Keep generic property/group identity/preflight integration here. The
	// actual carrier's NULL/error runtime demand lives in its local unit.
	if node.Properties().Volatility != expr.Volatile || !node.Properties().ParameterDependent || !node.Properties().RowDependent {
		t.Fatal("once-analyzed child properties", node.Properties())
	}
	bind := func(query string) expr.Expr {
		t.Helper()
		node, err := b.bind(statement(t, query).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	named := bind("SELECT to_ascii('é','LATIN1')")
	if !b.sameGroupExpression(named, bind("SELECT to_ascii('é','LATIN1')")) || b.sameGroupExpression(named, bind("SELECT to_ascii('é',8)")) {
		t.Fatal("overload structural identity")
	}
	folded, changed, err := preflightCoercions(&EvalContext{Context: context.Background()}, bind("SELECT unistr('plain')"))
	if err != nil || !changed {
		t.Fatal("immutable preflight", folded, changed, err)
	}
}
