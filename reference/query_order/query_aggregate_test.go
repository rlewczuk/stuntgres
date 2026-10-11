package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryAggregateRegressionSteps() []fixture.Step {
	steps := []fixture.Step{
		fixture.Select("SELECT k,count(*) FROM t GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		fixture.Select("SELECT count(*) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT k,count(*) FROM empty GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, nil),
		fixture.Select("SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}}),
		fixture.Select("SELECT k FROM t GROUP BY k HAVING count(*)>1", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}}),
		fixture.Select("SELECT 7 FROM empty HAVING TRUE", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Select("SELECT (v+1)*2,count(*) FROM t GROUP BY v+1", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(4), int64(1)}, {nil, int64(1)}, {int64(6), int64(1)}}),
		fixture.Select("SELECT k,v,count(*) FROM t GROUP BY k,v", []string{"k", "v", "count"}, []string{"TEXT", "INT4", "INT8"}, [][]any{{"a", int64(1), int64(1)}, {"a", nil, int64(1)}, {nil, int64(2), int64(1)}}),
	}
	for i := range steps {
		steps[i].Bag = true
	}
	for _, tc := range []struct{ query, relation, position string }{
		{"SELECT v,count(*) FROM t", "t", "8"},
		{"SELECT /*é*/ v,count(*) FROM t", "t", "14"},
		{"SELECT v,count(*) FROM empty HAVING FALSE", "empty", "8"},
		{"SELECT v,count(*) FROM t GROUP BY v+1", "t", "8"},
	} {
		step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42803", Message: `column "` + tc.relation + `.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: tc.position})
		step.SelectRows = true
		step.ErrorStage = "execute"
		steps = append(steps, step)
	}
	return steps
}

func TestReferenceQueryAggregateRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0),
		fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3),
		fixture.Exec("CREATE TABLE empty(k TEXT,v INT)", 0),
	} {
		p.Step(t, step)
	}
	for _, step := range queryAggregateRegressionSteps() {
		t.Run(step.Query, func(t *testing.T) {
			p.Step(t, step)
			if step.Diagnostic != nil {
				step.ErrorStage = "prepare"
			}
			p.Step(t, fixture.Prepared(step))
		})
	}
}
