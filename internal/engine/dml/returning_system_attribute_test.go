package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"testing"
)

func TestReturningSystemAttributeDemand(t *testing.T) {
	target := plan.SystemAttributeExpr{SubID: -1, Scope: 1, Frame: 1}
	source := target
	source.QueryOccurrence = 1
	inner := target
	inner.Frame = 2
	generated := target
	generated.SubID, generated.Scope, generated.Frame = -6, 0, 0
	correlated := plan.SubqueryExpr{QueryPlan: &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{target}, Frame: 2}}}
	local := correlated
	local.QueryPlan = &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{inner}, Frame: 2}}
	analysisOnly := correlated
	analysisOnly.CTEEdge = true
	for _, tc := range []struct {
		name  string
		value expr.Expr
		want  bool
	}{
		{"target", target, true}, {"source", source, false}, {"generated", generated, false},
		{"correlated_target", correlated, true}, {"subquery_local", local, false}, {"analysis_only", analysisOnly, false},
		{"ordinary", plan.ColumnExpr{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := returningHasPhysicalHeaders(&analyze.Returning{Expressions: []expr.Expr{tc.value}}); got != tc.want {
				t.Fatal("wrong accepted-version demand", got, tc.want)
			}
		})
	}
	if returningHasPhysicalHeaders(nil) {
		t.Fatal("no RETURNING acquired an accepted-version carrier")
	}
}
