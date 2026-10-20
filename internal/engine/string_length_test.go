package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Typed-node units supplement public reference scenarios without SQL execution.
func TestStringLengthInputCollationAndResultAuthority(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	left, right := text.Usage(), text.Usage()
	left.Collation.ID, right.Collation.ID = 950, 951
	args := []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: left}, Value: sqltype.String(text, "abc")},
		plan.LiteralExpr{Info: expr.Info{Usage: right}, Value: sqltype.String(text, "")},
	}
	for _, name := range []string{"position", "strpos"} {
		node, err := b.stringNode(name, integer.Usage(), parser.Position{Character: 8}, args, [4]bool{true, true}, parser.OrdinaryStringSyntax)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(functions.String)
		if n.ResultUsage() != integer.Usage() || n.CollationIndeterminate() || n.ExplicitCollation().Character != 0 || !n.Plan.CollationIndeterminate() {
			t.Fatal("input conflict leaked onto INT4 result", n)
		}
		_, err = n.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("empty needle bypassed input demand", err)
		}
		explicit := append([]expr.Expr(nil), args...)
		explicit[0] = plan.LiteralExpr{Info: expr.Info{Usage: left, CollationExplicit: parser.Position{Character: 20}}, Value: sqltype.String(text, "abc")}
		resolved, err := b.stringNode(name, integer.Usage(), parser.Position{}, explicit, [4]bool{true, true}, parser.OrdinaryStringSyntax)
		if err != nil || resolved.(functions.String).Plan.CollationIndeterminate() || resolved.ResultUsage() != integer.Usage() {
			t.Fatal(resolved, err)
		}
		value, err := resolved.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
		index, _ := value.Integer()
		if err != nil || index != 1 {
			t.Fatal(value, err)
		}
		explicit[1] = plan.LiteralExpr{Info: expr.Info{Usage: right, CollationExplicit: parser.Position{Character: 40}}, Value: sqltype.Null(text)}
		_, err = b.stringNode(name, integer.Usage(), parser.Position{}, explicit, [4]bool{true, true}, parser.OrdinaryStringSyntax)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P21" || diagnostic.Position != "40" {
			t.Fatal("explicit mismatch analysis", err)
		}
	}
	count, err := b.stringNode("length", integer.Usage(), parser.Position{}, args[:1], [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil || count.ResultUsage() != integer.Usage() || count.CollationIndeterminate() {
		t.Fatal("count result authority", count, err)
	}
}

func TestStringLengthRetainedPlansAndChildDemand(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	args := []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable, ParameterDependent: true}}, Value: sqltype.Null(text)},
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, Value: sqltype.String(text, "x")},
	}
	bound, err := b.stringNode("strpos", integer.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(functions.String)
	// Pure child demand/error order is local; retain genuine owner property merge.
	if node.Properties().Volatility != expr.Volatile || !node.Properties().RowDependent || !node.Properties().ParameterDependent {
		t.Fatal("child properties", node.Properties())
	}
	bpchar := b.typ(sqltype.BPChar)
	fixed := bpchar.Usage()
	fixed.Typmod = 8
	count, err := b.stringNode("octet_length", integer.Usage(), parser.Position{}, []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: fixed}, Value: sqltype.String(bpchar, "é🙂  ")}}, [4]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), count, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	retained := rebound.(functions.String)
	if retained.Plan.ArgumentUsage(0).Type != replacements[sqltype.BPChar] || retained.Plan.TextUsage().Type != replacements[sqltype.Text] || retained.ResultUsage().Type != replacements[sqltype.Int4] || count.(functions.String).Plan.ArgumentUsage(0).Type != bpchar {
		t.Fatal("retag mutated source or lost formal ownership", retained)
	}
	got, err := retained.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
	n, _ := got.Integer()
	if err != nil || n != 8 || got.Type() != replacements[sqltype.Int4] {
		t.Fatal(got, err)
	}
}
