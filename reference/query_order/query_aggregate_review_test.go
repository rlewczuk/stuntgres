package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAggregateReviewRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(v NUMERIC)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
	invalid := fixture.ImplicitError("SELECT v=1.0,count(*) FROM t GROUP BY v=1.00", fixture.Diagnostic{
		Code: "42803", Message: `column "t.v" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8",
	})
	invalid.SelectRows = true
	invalid.ErrorStage = "execute"
	control := fixture.Select("SELECT v=1.0,count(*) FROM t GROUP BY v=1.0", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {false, int64(1)}})
	control.Bag = true
	pgSibling := fixture.Select("SELECT count(*),CAST('0' AS regclass)", []string{"count", "regclass"}, []string{"INT8", "REGCLASS"}, [][]any{{int64(1), "-"}})

	if p.Postgres != nil {
		t.Run("postgres", func(t *testing.T) {
			for _, step := range []fixture.Step{invalid, control, pgSibling} {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
				if step.Diagnostic != nil {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
			}
		})
	}
	t.Run("native", func(t *testing.T) {
		for _, step := range []fixture.Step{invalid, control} {
			t.Run(step.Query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step)
				if step.Diagnostic != nil {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(step))
			})
		}
		t.Run("no prefix effects", func(t *testing.T) {
			failure := fixture.Error("INSERT INTO t VALUES(3);SELECT count(*) WHERE", "42601")
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, failure)
			queryAggregatePrepareRejection(t, p, failure.Query, failure.State)
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
	p.Step(t, pgSibling)
	p.Step(t, fixture.Prepared(pgSibling))
}
