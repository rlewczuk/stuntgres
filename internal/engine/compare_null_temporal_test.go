package engine

import (
	"context"
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
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(distinctExpression)
		beforeLeft, err := node.Left.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		beforeRight, err := node.Right.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		if len(b.prepared.temporals) != 2 || b.prepared.temporals[0].offset >= b.prepared.temporals[1].offset {
			t.Fatal("symmetric contextualization must retain sorted source captures", b.prepared.temporals)
		}
		description := StatementDescription{temporals: b.prepared.temporals}
		later := sqltype.InputEnvironment{TransactionStart: first.TransactionStart.Add(25 * time.Hour)}
		rebound := newTestExpressionBinder(t, ctx, e, later, &description)
		bound, err = rebound.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		node = bound.(distinctExpression)
		afterLeft, err := node.Left.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		afterRight, err := node.Right.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		equality, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, beforeLeft.Type(), afterLeft.Type(), b.typ(sqltype.Bool), sqltype.CollationRef{}, false)
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
