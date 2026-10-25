package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rp_defer_owner"})
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE rp_smoke(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE TABLE rp_smoke_a PARTITION OF rp_smoke FOR VALUES FROM(1) TO(3)", 0)))
		run(fixture.Exec("CREATE TABLE rp_smoke_b PARTITION OF rp_smoke FOR VALUES FROM(3) TO(5)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_smoke VALUES(1,'a'),(2,'b'),(3,'c'),(4,'d')", 4)))
		run(referenceRangeRows("SELECT k FROM rp_smoke_a ORDER BY k", 1, 2))
		run(referenceRangeRows("SELECT k FROM rp_smoke_b ORDER BY k", 3, 4))
		s := fixture.Select("SELECT * FROM rp_smoke ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(4), "d"}})
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
		run(s)
		run(fixture.Prepared(s))
		run(referenceRangeMiss("INSERT INTO rp_smoke VALUES(1,'no'),(3,'no'),(5,'no')", "rp_smoke", "k", "5"))
		run(s)
		run(fixture.Exec("CREATE TABLE rp_smoke_def PARTITION OF rp_smoke DEFAULT", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_smoke VALUES($1,$2),(NULL,'null')", 2, 5, "five")))
		run(fixture.Select("SELECT k,payload FROM rp_smoke ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(4), "d"}, {int64(5), "five"}, {nil, "null"}}))
		run(referenceRangeError("CREATE TABLE rp_smoke_conflict PARTITION OF rp_smoke FOR VALUES FROM(5) TO(7)", "23514", `updated partition constraint for default partition "rp_smoke_def" would be violated by some row`, fixture.Diagnostic{Schema: "public", Table: "rp_smoke_def"}))
		run(fixture.Select("SELECT * FROM rp_smoke_def ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), "five"}, {nil, "null"}}))
		referenceRangeNested(t, p, b)
		referenceRangeControlBody(t, p, b)
		referenceRangeDeferredControlBody(t, p, b)
	})
}
