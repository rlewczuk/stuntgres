package engine

import (
	"context"
	"testing"

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
			binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
			node, err := binder.bind(castTarget(t, "SELECT "+expression+" FROM t"), expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			// Immutable is the optimizer's constant-evaluation admission criterion;
			// these literal-only trees must not qualify across session changes.
			if got := node.Properties(); got != (ExprProperties{Volatility: Stable}) {
				t.Fatalf("properties = %+v, want session-dependent Stable without other dependencies", got)
			}
			if _, ok := node.(conditionalExpression); ok {
				planned, _, err := simplifyConditional(&EvalContext{Context: ctx}, node, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, folded := planned.(literalExpression); folded {
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
			want    ExprProperties
		}{
			{"TIMESTAMP '2004-10-19 00:00'", ExprProperties{Volatility: Stable}},
			{"CASE WHEN nextval($1)>0 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END", ExprProperties{Volatility: Volatile, ParameterDependent: true}},
		} {
			t.Run(tc.operand+" "+suffix, func(t *testing.T) {
				binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
				node, err := binder.bind(castTarget(t, "SELECT ("+tc.operand+") "+suffix+" FROM t"), expressionPolicy{context: projectionContext})
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
