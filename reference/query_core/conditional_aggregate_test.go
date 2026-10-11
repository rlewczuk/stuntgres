package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalAggregateComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(v INTEGER)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE empty(v INTEGER)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE out(v BIGINT)", 0))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END,COALESCE(count(*),0),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM t", []string{"case", "coalesce", "nullif", "greatest", "least"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(3), int64(3), int64(3), int64(2)}}),
		fixture.Select("SELECT CASE WHEN count(*)=3 THEN 7 ELSE 0 END FROM t HAVING COALESCE(count(*),0)>0", []string{"case"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Select("SELECT CASE WHEN v=1 THEN 10 ELSE 20 END,count(*) FROM t GROUP BY CASE WHEN v=1 THEN 10 ELSE 20 END", []string{"case", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(2)}, {int64(20), int64(1)}}),
		fixture.Select("SELECT COALESCE(v,0),count(*) FROM t GROUP BY COALESCE(v,0)", []string{"coalesce", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}),
		fixture.Select("SELECT CASE count(*) WHEN 0 THEN count(*) ELSE 9 END,COALESCE(count(*),9),NULLIF(count(*),0),GREATEST(count(*),2),LEAST(count(*),2) FROM empty", []string{"case", "coalesce", "nullif", "greatest", "least"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(0), int64(0), nil, int64(2), int64(0)}}),
		fixture.Select("SELECT COALESCE(v,0),count(*) FROM empty GROUP BY COALESCE(v,0)", []string{"coalesce", "count"}, []string{"INT4", "INT8"}, [][]any{}),
	} {
		step.Bag = true
		fixture.ScalarStep(t, p, step)
		for _, backend := range p.Backends() {
			held, err := backend.DB.PrepareContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}()
			for range 2 {
				fixture.ScalarRunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, held, step.Query}, backend.Recorder, step)
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, q := range []string{
		"INSERT INTO out SELECT CASE count(*) WHEN 3 THEN count(*) ELSE 0 END FROM t",
		"INSERT INTO out SELECT COALESCE(count(*),9) FROM empty",
	} {
		step := fixture.Exec(q, 1)
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	fixture.ScalarStep(t, p, fixture.Select("SELECT v FROM out ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}, {int64(0)}, {int64(3)}, {int64(3)}}))
	for _, tc := range []struct {
		q          string
		diagnostic fixture.Diagnostic
	}{
		{"SELECT CASE WHEN TRUE THEN count(*) ELSE v END FROM t", fixture.Diagnostic{Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "42"}},
		{"SELECT COALESCE(count(*),0) FROM t GROUP BY 1", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "17"}},
		{"SELECT count(*) FROM t WHERE COALESCE(count(*),0)>0", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "39"}},
	} {
		step := fixture.ImplicitError(tc.q, tc.diagnostic)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}
