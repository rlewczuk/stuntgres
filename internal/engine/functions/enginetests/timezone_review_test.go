package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestTimezoneReviewConditionalProperties(t *testing.T) {
	ctx := context.Background()
	for _, expression := range []string{
		"NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00')",
		"NULLIF(TIMESTAMPTZ '2004-10-19 04:00+00',DATE '2004-10-19')",
		"CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END",
		"CASE TIMESTAMPTZ '2004-10-19 04:00+00' WHEN DATE '2004-10-19' THEN TRUE ELSE FALSE END",
		"CASE WHEN TRUE THEN DATE '2004-10-19' ELSE TIMESTAMPTZ '2004-10-19 04:00+00' END",
	} {
		t.Run(expression, func(t *testing.T) {
			binder := enginetest.ExpressionBinder(t, ctx, engine.New(), sqltype.InputEnvironment{}, nil)
			node, err := binder.Bind(castTarget(t, "SELECT "+expression+" FROM t"), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			// Immutable is the optimizer's constant-evaluation admission criterion;
			// these literal-only trees must not qualify across session changes.
			if got := node.Properties(); got != (expr.Properties{Volatility: expr.Stable}) {
				t.Fatalf("properties = %+v, want session-dependent Stable without other dependencies", got)
			}
			if _, ok := node.(plan.ConditionalExpr); ok {
				planned, _, err := exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, node, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, folded := planned.(plan.LiteralExpr); folded {
					t.Fatal("session-dependent NULLIF admitted to immutable constant evaluation")
				}
			}
		})
	}
}

func TestTimezoneReviewATVolatility(t *testing.T) {
	ctx := context.Background()
	for _, suffix := range []string{"AT LOCAL", "AT TIME ZONE 'UTC'"} {
		for _, tc := range []struct {
			operand string
			want    expr.Properties
		}{
			{"TIMESTAMP '2004-10-19 00:00'", expr.Properties{Volatility: expr.Stable}},
			{"CASE WHEN nextval($1)>0 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END", expr.Properties{Volatility: expr.Volatile, ParameterDependent: true}},
		} {
			t.Run(tc.operand+" "+suffix, func(t *testing.T) {
				binder := enginetest.ExpressionBinder(t, ctx, engine.New(), sqltype.InputEnvironment{}, nil)
				node, err := binder.Bind(castTarget(t, "SELECT ("+tc.operand+") "+suffix+" FROM t"), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				if got := node.Properties(); got != tc.want {
					t.Fatalf("properties = %+v, want %+v: AT must not make a sequence-producing operand eligible for stable evaluation", got, tc.want)
				}
			})
		}
	}
}
