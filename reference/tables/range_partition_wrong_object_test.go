package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionWrongObjectDiagnostic(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
		run(fixture.Exec("CREATE TABLE rp_wrong_root(k INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE rp_wrong_leaf PARTITION OF rp_wrong_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO rp_wrong_root VALUES(1)", 1))
		for _, prepared := range []bool{false, true} {
			for _, name := range []string{"rp_wrong_root", "rp_wrong_leaf"} {
				step := referenceRangeError("DROP INDEX "+name, "42809", `"`+name+`" is not an index`, fixture.Diagnostic{Hint: "Use DROP TABLE to remove a table."})
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
			}
			step := fixture.Select("SELECT k FROM rp_wrong_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
		}
	})
}
