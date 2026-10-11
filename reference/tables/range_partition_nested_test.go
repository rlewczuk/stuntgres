package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceRangeNested(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE rp_nested(k INT,payload INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Prepared(fixture.Exec("CREATE TABLE rp_mid PARTITION OF rp_nested FOR VALUES FROM(1) TO(5) PARTITION BY RANGE(payload)", 0)))
	run(referenceRangeMiss("INSERT INTO rp_nested VALUES(1,10)", "rp_mid", "payload", "10"))
	run(fixture.Exec("CREATE TABLE rp_deep PARTITION OF rp_mid FOR VALUES FROM(10) TO(20)", 0))
	run(fixture.Exec("CREATE TABLE rp_mid_def PARTITION OF rp_mid DEFAULT", 0))
	run(fixture.Exec("CREATE TABLE rp_outer_def PARTITION OF rp_nested DEFAULT PARTITION BY RANGE((payload+1))", 0))
	run(fixture.Exec("CREATE TABLE rp_outer_low PARTITION OF rp_outer_def FOR VALUES FROM(MINVALUE) TO(20)", 0))
	run(fixture.Exec("CREATE TABLE rp_outer_rest PARTITION OF rp_outer_def DEFAULT", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO rp_nested VALUES(1,10),(1,10),(2,19),(3,20),(4,NULL),(5,10),(NULL,10),(8,NULL)", 8)))
	all := [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(19)}, {int64(3), int64(20)}, {int64(4), nil}, {int64(5), int64(10)}, {int64(8), nil}, {nil, int64(10)}}
	run(fixture.Select("SELECT * FROM rp_nested ORDER BY k NULLS LAST,payload", []string{"k", "payload"}, []string{"INT4", "INT4"}, all))
	run(fixture.Select("SELECT * FROM rp_mid ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "INT4"}, all[:5]))
	run(fixture.Select("SELECT * FROM rp_deep ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "INT4"}, all[:3]))
	run(referenceRangeLeafMiss("INSERT INTO rp_deep VALUES(5,10)", "rp_deep", "5, 10"))
	run(referenceRangeLeafMiss("INSERT INTO rp_mid VALUES(5,10)", "rp_mid", "5, 10"))
	run(referenceRangeLeafMiss("INSERT INTO rp_mid_def VALUES(2,10)", "rp_mid_def", "2, 10"))
	run(referenceRangeError("CREATE TABLE rp_new PARTITION OF rp_nested FOR VALUES FROM(5) TO(7)", "23514", `updated partition constraint for default partition "rp_outer_def" would be violated by some row`, fixture.Diagnostic{Schema: "public", Table: "rp_outer_def"}))
	run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_new'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(fixture.Select("SELECT * FROM rp_nested ORDER BY k NULLS LAST,payload", []string{"k", "payload"}, []string{"INT4", "INT4"}, all))
	// Dropping a DEFAULT descendant removes its rows, not a fake virtual store.
	run(fixture.Exec("DROP TABLE rp_outer_low", 0))
	run(fixture.Exec("CREATE TABLE rp_new PARTITION OF rp_nested FOR VALUES FROM(5) TO(7)", 0))
	run(referenceRangeLeafMiss("INSERT INTO rp_outer_rest VALUES(5,NULL)", "rp_outer_rest", "5, null"))
	run(fixture.Exec("INSERT INTO rp_nested VALUES(5,99)", 1))
	run(fixture.Exec("CREATE TABLE rp_excess(k INT,payload INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_excess_mid PARTITION OF rp_excess FOR VALUES FROM(1) TO(5) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_excess_leaf PARTITION OF rp_excess_mid FOR VALUES FROM(0) TO(10)", 0))
	run(fixture.Exec("INSERT INTO rp_excess VALUES(2,20)", 1))
	run(referenceRangeLeafMiss("INSERT INTO rp_excess_leaf VALUES(6,60)", "rp_excess_leaf", "6, 60"))
	run(fixture.Select("SELECT * FROM rp_excess", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
}

func TestReferenceRangePartitionNested(t *testing.T) {
	referenceRangeScenario(t, referenceRangeNested)
}
