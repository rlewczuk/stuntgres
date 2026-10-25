package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticalAggregateWindowSignatures(t *testing.T) {
	for _, name := range []string{"var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev", "regr_slope", "regr_sxx", "regr_sxy", "regr_syy",
		"corr", "covar_pop", "covar_samp", "regr_avgx", "regr_avgy", "regr_count", "regr_intercept", "regr_r2"} {
		valid, unknown, invalid := "1", "NULL", "TRUE"
		if regressionAggregateName(name) {
			valid, unknown, invalid = "2,1", "NULL,NULL", "1,TRUE"
		}
		for _, tc := range []struct {
			args, code string
			result     sqltype.ID
		}{
			{valid, "", sqltype.Numeric},
			{unknown, "", sqltype.Float8},
			{invalid, "42883", 0},
			{"", "42883", 0},
			{"1,2,3", "42883", 0},
		} {
			t.Run(name+"("+tc.args+")", func(t *testing.T) {
				raw := statement(t, "SELECT "+name+"("+tc.args+") OVER ()").(*parser.Select).Targets[0].(parser.AggregateExpr)
				e := New()
				b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				b.Clause.AggregateClause = "SELECT"
				e.Mu.Lock()
				defer e.Mu.Unlock()
				seed := statement(t, "SELECT count(*)").(*parser.Select).Targets[0]
				if _, err := b.Bind(seed, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
					t.Fatal(err)
				}
				before := append([]plan.AggregateCall(nil), b.Grouping.Aggregates...)
				callee := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
				var argument expr.Expr
				usage, err := b.ResolveWindowAggregateSignature(raw, callee, &argument)
				if tc.code == "" {
					want := tc.result
					if regressionAggregateName(name) {
						want = sqltype.Float8
					}
					if name == "regr_count" {
						want = sqltype.Int8
					}
					if err != nil || usage.Type == nil || usage.Type.ID() != want || usage.Typmod != -1 || argument == nil {
						t.Fatal(usage, argument, err)
					}
				} else {
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != tc.code {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(before, b.Grouping.Aggregates) || len(b.Grouping.WindowCalls) != 0 || b.Grouping.ArgumentOwner != (analyze.ArgumentWindowOwner{}) {
					t.Fatal("excluded signature analysis registered group/window state or retained argument ownership")
				}
			})
		}
	}
}

// regressionAggregateName reports the two-argument statistical aggregates.
func regressionAggregateName(name string) bool {
	return strings.HasPrefix(name, "regr_") || name == "corr" || strings.HasPrefix(name, "covar_")
}
