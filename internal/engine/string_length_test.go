package engine

import (
	"context"
	"errors"
	"reflect"
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
	args := []BoundExpr{
		literalExpression{expressionInfo{usage: left}, sqltype.String(text, "abc")},
		literalExpression{expressionInfo{usage: right}, sqltype.String(text, "")},
	}
	for _, name := range []string{"position", "strpos"} {
		node, err := b.stringNode(name, integer.Usage(), parser.Position{Character: 8}, args, [2]bool{true, true}, parser.OrdinaryStringSyntax)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(stringExpression)
		if n.ResultUsage() != integer.Usage() || n.CollationIndeterminate() || n.ExplicitCollation().Character != 0 || !n.plan.CollationIndeterminate() {
			t.Fatal("input conflict leaked onto INT4 result", n)
		}
		_, err = n.Eval(&EvalContext{Context: context.Background()})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
			t.Fatal("empty needle bypassed input demand", err)
		}
		explicit := append([]BoundExpr(nil), args...)
		explicit[0] = literalExpression{expressionInfo{usage: left, collationExplicit: parser.Position{Character: 20}}, sqltype.String(text, "abc")}
		resolved, err := b.stringNode(name, integer.Usage(), parser.Position{}, explicit, [2]bool{true, true}, parser.OrdinaryStringSyntax)
		if err != nil || resolved.(stringExpression).plan.CollationIndeterminate() || resolved.ResultUsage() != integer.Usage() {
			t.Fatal(resolved, err)
		}
		value, err := resolved.Eval(&EvalContext{Context: context.Background()})
		index, _ := value.Integer()
		if err != nil || index != 1 {
			t.Fatal(value, err)
		}
		explicit[1] = literalExpression{expressionInfo{usage: right, collationExplicit: parser.Position{Character: 40}}, sqltype.Null(text)}
		_, err = b.stringNode(name, integer.Usage(), parser.Position{}, explicit, [2]bool{true, true}, parser.OrdinaryStringSyntax)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P21" || diagnostic.Position != "40" {
			t.Fatal("explicit mismatch analysis", err)
		}
	}
	count, err := b.stringNode("length", integer.Usage(), parser.Position{}, args[:1], [2]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil || count.ResultUsage() != integer.Usage() || count.CollationIndeterminate() {
		t.Fatal("count result authority", count, err)
	}
}

func TestStringLengthRetainedPlansAndChildDemand(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	var order []int
	args := []BoundExpr{
		mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage(), properties: ExprProperties{Volatility: Stable, ParameterDependent: true}}, value: sqltype.Null(text), order: &order, index: 0},
		mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage(), properties: ExprProperties{Volatility: Volatile, RowDependent: true}}, value: sqltype.String(text, "x"), order: &order, index: 1},
	}
	bound, err := b.stringNode("strpos", integer.Usage(), parser.Position{}, args, [2]bool{true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(stringExpression)
	got, err := node.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) || node.Properties().Volatility != Volatile || !node.Properties().RowDependent || !node.Properties().ParameterDependent {
		t.Fatal("child demand/properties", got, order, node.Properties(), err)
	}
	failure := errors.New("demanded search sibling")
	node.args[1] = mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&EvalContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	bpchar := b.typ(sqltype.BPChar)
	fixed := bpchar.Usage()
	fixed.Typmod = 8
	count, err := b.stringNode("octet_length", integer.Usage(), parser.Position{}, []BoundExpr{literalExpression{expressionInfo{usage: fixed}, sqltype.String(bpchar, "é🙂  ")}}, [2]bool{true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(count, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	retained := rebound.(stringExpression)
	if retained.plan.ArgumentUsage(0).Type != replacements[sqltype.BPChar] || retained.plan.TextUsage().Type != replacements[sqltype.Text] || retained.ResultUsage().Type != replacements[sqltype.Int4] || count.(stringExpression).plan.ArgumentUsage(0).Type != bpchar {
		t.Fatal("retag mutated source or lost formal ownership", retained)
	}
	got, err = retained.Eval(&EvalContext{Context: context.Background()})
	n, _ := got.Integer()
	if err != nil || n != 8 || got.Type() != replacements[sqltype.Int4] {
		t.Fatal(got, err)
	}
}
