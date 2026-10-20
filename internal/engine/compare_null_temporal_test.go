package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestExpressionCompareNullTemporalCaptureOrderAndFreeze(t *testing.T) {
	e := New()
	ctx := context.Background()
	first := sqltype.InputEnvironment{TransactionStart: time.Date(2024, 12, 31, 12, 34, 56, 0, time.UTC)}
	for _, q := range []string{"SELECT 'now' IS DISTINCT FROM TIMESTAMP 'now' FROM t", "SELECT TIMESTAMP 'now' IS NOT DISTINCT FROM 'now' FROM t", "SELECT 'now' IS DISTINCT FROM TIME 'now' FROM t"} {
		raw := statement(t, q).(*parser.Select).Targets[0]
		b := newTestExpressionBinder(t, ctx, e, first, nil)
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(plan.DistinctExpr)
		beforeLeft, err := node.Left.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		beforeRight, err := node.Right.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Prepared.Temporals) != 2 || b.Prepared.Temporals[0].Offset >= b.Prepared.Temporals[1].Offset {
			t.Fatal("symmetric contextualization must retain sorted source captures", b.Prepared.Temporals)
		}
		description := StatementDescription{temporals: b.Prepared.Temporals}
		later := sqltype.InputEnvironment{TransactionStart: first.TransactionStart.Add(25 * time.Hour)}
		rebound := newTestExpressionBinder(t, ctx, e, later, &description)
		bound, err = rebound.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node = bound.(plan.DistinctExpr)
		afterLeft, err := node.Left.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		afterRight, err := node.Right.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		equality, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, beforeLeft.Type(), afterLeft.Type(), b.Type(sqltype.Bool), sqltype.CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]sqltype.Value{{beforeLeft, afterLeft}, {beforeRight, afterRight}} {
			equal, err := equality.Evaluate(ctx, pair[0], pair[1], sqltype.InputEnvironment{})
			truth, truthErr := plan.TruthOf(equal)
			if err != nil || truthErr != nil || truth != plan.True {
				t.Fatal("prepared operand reread transaction clock", pair, err, truthErr)
			}
		}
	}
}
