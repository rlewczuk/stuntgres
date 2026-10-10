package reference_test

import "testing"

func TestReferenceRangePartitionFeatureSmoke(t *testing.T) {
	roleReferenceCleanup(t, []string{"rp_defer_owner"})
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_smoke(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(referencePrepared(referenceExec("CREATE TABLE rp_smoke_a PARTITION OF rp_smoke FOR VALUES FROM(1) TO(3)", 0)))
		run(referenceExec("CREATE TABLE rp_smoke_b PARTITION OF rp_smoke FOR VALUES FROM(3) TO(5)", 0))
		run(referencePrepared(referenceExec("INSERT INTO rp_smoke VALUES(1,'a'),(2,'b'),(3,'c'),(4,'d')", 4)))
		run(referenceRangeRows("SELECT k FROM rp_smoke_a ORDER BY k", 1, 2))
		run(referenceRangeRows("SELECT k FROM rp_smoke_b ORDER BY k", 3, 4))
		s := referenceSelect("SELECT * FROM rp_smoke ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(4), "d"}})
		s.checkMetadata = referenceRangeMetadata(b.pg, s.columns, s.types, []bool{true, true})
		run(s)
		run(referencePrepared(s))
		run(referenceRangeMiss("INSERT INTO rp_smoke VALUES(1,'no'),(3,'no'),(5,'no')", "rp_smoke", "k", "5"))
		run(s)
		run(referenceExec("CREATE TABLE rp_smoke_def PARTITION OF rp_smoke DEFAULT", 0))
		run(referencePrepared(referenceExec("INSERT INTO rp_smoke VALUES($1,$2),(NULL,'null')", 2, 5, "five")))
		run(referenceSelect("SELECT k,payload FROM rp_smoke ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(4), "d"}, {int64(5), "five"}, {nil, "null"}}))
		run(referenceRangeError("CREATE TABLE rp_smoke_conflict PARTITION OF rp_smoke FOR VALUES FROM(5) TO(7)", "23514", `updated partition constraint for default partition "rp_smoke_def" would be violated by some row`, referenceDiagnostic{Schema: "public", Table: "rp_smoke_def"}))
		run(referenceSelect("SELECT * FROM rp_smoke_def ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(5), "five"}, {nil, "null"}}))
		referenceRangeNested(t, p, b)
		referenceRangeControlBody(t, p, b)
		referenceRangeDeferredControlBody(t, p, b)
	})
}
