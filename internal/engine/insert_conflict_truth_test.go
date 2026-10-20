package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func insertConflictTruthSubject(f insertConflictProofFixture) columnExpression {
	return columnExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
}
func insertConflictTruthTest(f insertConflictProofFixture, child expr.Expr, truth Truth, not bool) truthTestExpression {
	return truthTestExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Child: child, Truth: truth, Not: not}
}
func insertConflictNullTest(f insertConflictProofFixture, child expr.Expr, not bool) nullTestExpression {
	return nullTestExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Child: child, Not: not}
}
func insertConflictNot(f insertConflictProofFixture, child expr.Expr) logicalExpression {
	return logicalExpression{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Op: parser.LogicalNot, Left: child}
}
func TestInsertConflictPredicateNestedTruthTests(t *testing.T) {
	f := insertConflictProofTypes(t)
	subject := insertConflictTruthSubject(f)
	isTrue := insertConflictTruthTest(f, subject, True, false)
	isFalse := insertConflictTruthTest(f, subject, False, false)
	nonnull := insertConflictNullTest(f, subject, true)
	for _, c := range []struct {
		name string
		a, b expr.Expr
		want bool
	}{
		{"NOT IS TRUE", insertConflictNot(f, isTrue), nonnull, false},
		{"NOT IS FALSE", insertConflictNot(f, isFalse), nonnull, false},
		{"nonnull truth", insertConflictNullTest(f, isTrue, true), nonnull, false},
		{"nonnull false", insertConflictNullTest(f, isFalse, true), nonnull, false},
		{"NOT null truth", insertConflictNot(f, insertConflictNullTest(f, isTrue, false)), nonnull, false},
		{"false truth", insertConflictTruthTest(f, isTrue, False, false), nonnull, false},
		// Root's shared oracle rejects these qualification-equivalent forms.
		{"NOT IS NOT TRUE", insertConflictNot(f, insertConflictTruthTest(f, subject, True, true)), nonnull, false},
		{"NOT IS NOT FALSE", insertConflictNot(f, insertConflictTruthTest(f, subject, False, true)), nonnull, false},
		{"TRUE qualification", isTrue, subject, false},
		{"FALSE qualification", isFalse, insertConflictNot(f, subject), false},
		{"nested identity", insertConflictNot(f, isTrue), insertConflictNot(f, isTrue), true},
		{"different subjects", isTrue, insertConflictTruthTest(f, columnExpression{Info: subject.Info, Scope: 2, Frame: 1}, True, false), false},
		{"proof-only negated comparison", insertConflictNot(f, f.comparison(t, "<=", f.column, f.integerLiteral(t, 0))), f.comparison(t, ">", f.column, f.integerLiteral(t, 0)), true},
	} {
		got, err := conflictPredicateImpliedBy(&f.ctx, c.a, c.b)
		if err != nil || got != c.want {
			t.Fatal(c.name, got, err, c.want)
		}
	}
}
func TestInsertConflictPredicateValueNormalization(t *testing.T) {
	f := insertConflictProofTypes(t)
	subject := insertConflictTruthSubject(f)
	isTrue := insertConflictTruthTest(f, subject, True, false)
	isFalse := insertConflictTruthTest(f, subject, False, false)
	falseLiteral := literalExpression{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, false)}
	ctx := f.ctx
	ctx.Scope, ctx.Frame = 1, 1
	inputs := [3]sqltype.Value{sqltype.Boolean(f.boolean, true), sqltype.Boolean(f.boolean, false), sqltype.Null(f.boolean)}
	for _, c := range []struct {
		name string
		node expr.Expr
		want [3]Truth
	}{
		{"IS TRUE", isTrue, [3]Truth{True, False, False}},
		{"IS FALSE", isFalse, [3]Truth{False, True, False}},
		{"NOT IS TRUE", insertConflictNot(f, isTrue), [3]Truth{False, True, True}},
		{"NOT IS FALSE", insertConflictNot(f, isFalse), [3]Truth{True, False, True}},
		{"truth nonnull", insertConflictNullTest(f, isTrue, true), [3]Truth{True, True, True}},
		{"false nonnull", insertConflictNullTest(f, isFalse, true), [3]Truth{True, True, True}},
		{"NOT truth null", insertConflictNot(f, insertConflictNullTest(f, isTrue, false)), [3]Truth{True, True, True}},
		{"false truth", insertConflictTruthTest(f, isTrue, False, false), [3]Truth{False, True, True}},
		{"truth equality", f.comparison(t, "=", isTrue, falseLiteral), [3]Truth{False, True, True}},
	} {
		normalized, err := normalizeConflictPredicate(&ctx, c.node)
		if err != nil {
			t.Fatal(c.name, err)
		}
		for i, input := range inputs {
			ctx.Row = []sqltype.Value{input}
			for _, node := range [2]expr.Expr{c.node, normalized} {
				value, err := node.Eval(&ctx)
				if err != nil {
					t.Fatal(c.name, i, err)
				}
				truth, err := TruthOf(value)
				if err != nil || truth != c.want[i] {
					t.Fatal(c.name, i, truth, err, c.want[i])
				}
			}
		}
	}
	// A normalized comparison must not carry an inverted operator name with the
	// old provider evaluator. These parent values detect that mismatch directly.
	comparison := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	node := insertConflictTruthTest(f, insertConflictNot(f, comparison), True, false)
	normalized, err := normalizeConflictPredicate(&ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	integerInputs := [3]sqltype.Value{f.integerLiteral(t, -1).Value, f.integerLiteral(t, 1).Value, sqltype.Null(f.integer)}
	want := [3]Truth{True, False, False}
	for i, input := range integerInputs {
		ctx.Row = []sqltype.Value{input}
		for _, expression := range [2]expr.Expr{node, normalized} {
			value, err := expression.Eval(&ctx)
			if err != nil {
				t.Fatal(i, err)
			}
			truth, err := TruthOf(value)
			if err != nil || truth != want[i] {
				t.Fatal("negated comparison truth parent", i, truth, err, want[i])
			}
		}
	}
}
