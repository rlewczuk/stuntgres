package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func insertConflictTruthSubject(f insertConflictProofFixture) plan.ColumnExpr {
	return plan.ColumnExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
}
func insertConflictTruthTest(f insertConflictProofFixture, child expr.Expr, truth plan.Truth, not bool) plan.TruthTestExpr {
	return plan.TruthTestExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Child: child, Truth: truth, Not: not}
}
func insertConflictNullTest(f insertConflictProofFixture, child expr.Expr, not bool) plan.NullTestExpr {
	return plan.NullTestExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Child: child, Not: not}
}
func insertConflictNot(f insertConflictProofFixture, child expr.Expr) plan.LogicalExpr {
	return plan.LogicalExpr{Info: expr.Info{Usage: f.boolean.Usage(), Property: child.Properties()}, Op: parser.LogicalNot, Left: child}
}
func TestInsertConflictPredicateNestedTruthTests(t *testing.T) {
	f := insertConflictProofTypes(t)
	subject := insertConflictTruthSubject(f)
	isTrue := insertConflictTruthTest(f, subject, plan.True, false)
	isFalse := insertConflictTruthTest(f, subject, plan.False, false)
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
		{"false truth", insertConflictTruthTest(f, isTrue, plan.False, false), nonnull, false},
		// Root's shared oracle rejects these qualification-equivalent forms.
		{"NOT IS NOT TRUE", insertConflictNot(f, insertConflictTruthTest(f, subject, plan.True, true)), nonnull, false},
		{"NOT IS NOT FALSE", insertConflictNot(f, insertConflictTruthTest(f, subject, plan.False, true)), nonnull, false},
		{"TRUE qualification", isTrue, subject, false},
		{"FALSE qualification", isFalse, insertConflictNot(f, subject), false},
		{"nested identity", insertConflictNot(f, isTrue), insertConflictNot(f, isTrue), true},
		{"different subjects", isTrue, insertConflictTruthTest(f, plan.ColumnExpr{Info: subject.Info, Scope: 2, Frame: 1}, plan.True, false), false},
		{"proof-only negated comparison", insertConflictNot(f, f.comparison(t, "<=", f.column, f.integerLiteral(t, 0))), f.comparison(t, ">", f.column, f.integerLiteral(t, 0)), true},
	} {
		got, err := ConflictPredicateImpliedBy(&f.ctx, c.a, c.b)
		if err != nil || got != c.want {
			t.Fatal(c.name, got, err, c.want)
		}
	}
}
func TestInsertConflictPredicateValueNormalization(t *testing.T) {
	f := insertConflictProofTypes(t)
	subject := insertConflictTruthSubject(f)
	isTrue := insertConflictTruthTest(f, subject, plan.True, false)
	isFalse := insertConflictTruthTest(f, subject, plan.False, false)
	falseLiteral := plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, false)}
	ctx := f.ctx
	ctx.Scope, ctx.Frame = 1, 1
	inputs := [3]sqltype.Value{sqltype.Boolean(f.boolean, true), sqltype.Boolean(f.boolean, false), sqltype.Null(f.boolean)}
	for _, c := range []struct {
		name string
		node expr.Expr
		want [3]plan.Truth
	}{
		{"IS TRUE", isTrue, [3]plan.Truth{plan.True, plan.False, plan.False}},
		{"IS FALSE", isFalse, [3]plan.Truth{plan.False, plan.True, plan.False}},
		{"NOT IS TRUE", insertConflictNot(f, isTrue), [3]plan.Truth{plan.False, plan.True, plan.True}},
		{"NOT IS FALSE", insertConflictNot(f, isFalse), [3]plan.Truth{plan.True, plan.False, plan.True}},
		{"truth nonnull", insertConflictNullTest(f, isTrue, true), [3]plan.Truth{plan.True, plan.True, plan.True}},
		{"false nonnull", insertConflictNullTest(f, isFalse, true), [3]plan.Truth{plan.True, plan.True, plan.True}},
		{"NOT truth null", insertConflictNot(f, insertConflictNullTest(f, isTrue, false)), [3]plan.Truth{plan.True, plan.True, plan.True}},
		{"false truth", insertConflictTruthTest(f, isTrue, plan.False, false), [3]plan.Truth{plan.False, plan.True, plan.True}},
		{"truth equality", f.comparison(t, "=", isTrue, falseLiteral), [3]plan.Truth{plan.False, plan.True, plan.True}},
	} {
		normalized, err := NormalizeConflictPredicate(&ctx, c.node)
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
				truth, err := plan.TruthOf(value)
				if err != nil || truth != c.want[i] {
					t.Fatal(c.name, i, truth, err, c.want[i])
				}
			}
		}
	}
	// A normalized comparison must not carry an inverted operator name with the
	// old provider evaluator. These parent values detect that mismatch directly.
	comparison := f.comparison(t, ">", f.column, f.integerLiteral(t, 0))
	node := insertConflictTruthTest(f, insertConflictNot(f, comparison), plan.True, false)
	normalized, err := NormalizeConflictPredicate(&ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	integerInputs := [3]sqltype.Value{f.integerLiteral(t, -1).Value, f.integerLiteral(t, 1).Value, sqltype.Null(f.integer)}
	want := [3]plan.Truth{plan.True, plan.False, plan.False}
	for i, input := range integerInputs {
		ctx.Row = []sqltype.Value{input}
		for _, expression := range [2]expr.Expr{node, normalized} {
			value, err := expression.Eval(&ctx)
			if err != nil {
				t.Fatal(i, err)
			}
			truth, err := plan.TruthOf(value)
			if err != nil || truth != want[i] {
				t.Fatal("negated comparison truth parent", i, truth, err, want[i])
			}
		}
	}
}
