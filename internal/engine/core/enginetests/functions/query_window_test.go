package functions_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowReview3SignatureAnalysisNoRegistration(t *testing.T) {
	for _, tc := range []struct{ call, code string }{
		{"bool_and(true)", ""}, {"bool_and(1)", "42883"},
		{"bool_or(false)", ""}, {"bool_or(1)", "42883"},
		{"every(true)", ""}, {"every(1)", "42883"},
		{"avg(1)", ""}, {"avg(true)", "42883"}, {"avg(NULL)", "42725"},
		{"min(1)", ""}, {"min(true)", "42883"},
		{"max(1)", ""}, {"max(true)", "42883"},
		{"any_value(1)", ""}, {"any_value(1,2)", "42883"}, {"any_value(NULL)", "42804"},
		{"string_agg('x','|')", ""}, {"string_agg(1,2)", "42883"},
		{"sum(1)", ""}, {"sum(true)", "42883"}, {"sum()", "42883"}, {"sum(1,2)", "42883"}, {"sum(NULL)", "42725"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			raw := enginetest.Statement(t, "SELECT "+tc.call+" OVER ()").(*parser.Select).Targets[0].(parser.AggregateExpr)
			seed := enginetest.Statement(t, "SELECT count(*)").(*parser.Select).Targets[0]
			e := engine.New()
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			e.Mu.Lock()
			defer e.Mu.Unlock()
			if _, err := b.Bind(seed, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
			before := append([]plan.AggregateCall(nil), b.Grouping.Aggregates...)
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: raw.Name, Pos: raw.Pos}}}
			var argument expr.Expr
			_, err := b.ResolveWindowAggregateSignature(raw, name, &argument)
			var diagnostic *sqlstate.Error
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, b.Grouping.Aggregates) || len(b.Grouping.WindowCalls) != 0 || b.Grouping.ArgumentOwner != (analyze.ArgumentWindowOwner{}) {
				t.Fatal("signature analysis altered ordinary/provider/window state", before, b.Grouping.Aggregates, b.Grouping.WindowCalls, b.Grouping.ArgumentOwner)
			}
		})
	}
}
