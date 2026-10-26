package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestQueryPreflightAnalysisReplay(t *testing.T) {
	e, query, dependencies := analysisOnlyFixture(t, "SELECT CASE WHEN FALSE THEN 1/0 ELSE 7 END AS v")
	project := query.Root.(*plan.ProjectNode)
	original, ok := project.Expressions[0].(plan.CaseExpr)
	if !ok || len(original.Arms) != 1 {
		t.Fatal("expected retained CASE with its dead arithmetic child", project.Expressions[0])
	}
	// A matching position alone is insufficient: both a different expansion
	// and a structurally different expression must retain their own carriers.
	expansion := original
	expansion.Occurrence++
	different := original
	otherwise, ok := original.Otherwise.(plan.LiteralExpr)
	if !ok {
		t.Fatal("expected literal CASE fallback", original.Otherwise)
	}
	value, err := sqltype.Integer(otherwise.Usage.Type, 8)
	if err != nil {
		t.Fatal(err)
	}
	otherwise.Value = value
	different.Otherwise = otherwise
	project.AnalysisExpressions = []expr.Expr{original, expansion, different, original.Arms[0].Result}
	before := analysisOnlySnapshot(query.Root, dependencies)
	for range 2 {
		c := exec.Context{
			EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead(), PlanStaticOperators: true},
			EvalFrame:     exec.EvalFrame{Scope: 1, Frame: project.Frame},
		}
		prepared, err := view.PreflightQuery(&c, project)
		if err != nil {
			t.Fatal("analysis-only arithmetic advanced the value-preparation frontier", err)
		}
		private := prepared.(*plan.ProjectNode)
		if private == project || reflect.DeepEqual(private.Expressions[0], original) {
			t.Fatal("executable CASE was not privately simplified")
		}
		if !reflect.DeepEqual(private.AnalysisExpressions[0], private.Expressions[0]) {
			t.Fatal("matching ancillary original did not borrow completed executable planning")
		}
		for i := 1; i < len(project.AnalysisExpressions); i++ {
			if !reflect.DeepEqual(private.AnalysisExpressions[i], project.AnalysisExpressions[i]) {
				t.Fatal("unmatched ancillary occurrence was changed", i)
			}
		}
		analysisOnlyUnchanged(t, before, query.Root, dependencies)
	}
}
