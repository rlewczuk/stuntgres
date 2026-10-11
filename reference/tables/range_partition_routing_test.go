package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceRangeRouting(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE rp_route(k INT,payload TEXT DEFAULT 'parent') PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE rp_a PARTITION OF rp_route(payload DEFAULT 'leaf') FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE rp_b PARTITION OF rp_route FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("INSERT INTO rp_route(k,payload) VALUES(1,'one'),(2,'two'),(3,'three'),(4,'four')", 4))
	run(referenceRangeRows("SELECT k FROM rp_a ORDER BY k", 1, 2))
	run(referenceRangeRows("SELECT k FROM rp_b ORDER BY k", 3, 4))
	for _, c := range []struct{ value, detail string }{{"0", "0"}, {"5", "5"}, {"NULL", "null"}} {
		run(referenceRangeMiss("INSERT INTO rp_route(k) VALUES("+c.value+")", "rp_route", "k", c.detail))
	}
	run(referenceRangeMiss("INSERT INTO rp_route(k) VALUES(1),(3),(5)", "rp_route", "k", "5"))
	run(referenceRangeRows("SELECT k FROM rp_route ORDER BY k", 1, 2, 3, 4))
	run(fixture.Exec("INSERT INTO rp_route(k) VALUES(1)", 1))
	run(fixture.Exec("INSERT INTO rp_a(k) VALUES(2)", 1))
	run(fixture.Select("SELECT payload FROM rp_route WHERE payload='parent' OR payload='leaf' ORDER BY payload", []string{"payload"}, []string{"TEXT"}, [][]any{{"leaf"}, {"parent"}}))
	run(fixture.Exec("CREATE TABLE rp_def PARTITION OF rp_route DEFAULT", 0))
	run(fixture.Exec("INSERT INTO rp_route(k,payload) VALUES(0,'zero'),(5,'five'),(NULL,'null')", 3))
	run(fixture.Select("SELECT * FROM rp_def ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "zero"}, {int64(5), "five"}, {nil, "null"}}))
	run(referenceRangeError("CREATE TABLE rp_conflict PARTITION OF rp_route FOR VALUES FROM(5) TO(7)", "23514", `updated partition constraint for default partition "rp_def" would be violated by some row`, fixture.Diagnostic{Schema: "public", Table: "rp_def"}))
	run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_conflict'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(referenceRangeRows("SELECT k FROM rp_route WHERE k IS NOT NULL ORDER BY k", 0, 1, 1, 2, 2, 3, 4, 5))
}

func TestReferenceRangePartitionRouting(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { referenceRangeRouting(t, p, b, prepared) })
		})
	}
}

func TestReferenceRangePartitionConstraintsAndLeafIndexes(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE rp_check(k INT,payload INT NOT NULL,CONSTRAINT rp_positive CHECK(payload>0)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE rp_check_a PARTITION OF rp_check FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE rp_check_b PARTITION OF rp_check FOR VALUES FROM(3) TO(5)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX rp_local ON rp_check_b(payload)", 0))
		run(fixture.Exec("INSERT INTO rp_check VALUES(3,30)", 1))
		run(referenceRangeError("INSERT INTO rp_check VALUES(1,10),(4,30)", "23505", `duplicate key value violates unique constraint "rp_local"`, fixture.Diagnostic{Detail: "Key (payload)=(30) already exists.", Schema: "public", Table: "rp_check_b", Constraint: "rp_local"}))
		run(referenceRangeError("INSERT INTO rp_check VALUES(1,10),(3,NULL)", "23502", `null value in column "payload" of relation "rp_check_b" violates not-null constraint`, fixture.Diagnostic{Detail: "Failing row contains (3, null).", Schema: "public", Table: "rp_check_b", Column: "payload"}))
		run(referenceRangeError("INSERT INTO rp_check VALUES(1,10),(4,0)", "23514", `new row for relation "rp_check_b" violates check constraint "rp_positive"`, fixture.Diagnostic{Detail: "Failing row contains (4, 0).", Schema: "public", Table: "rp_check_b", Constraint: "rp_positive"}))
		run(fixture.Select("SELECT * FROM rp_check ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}}))
		run(fixture.Exec("INSERT INTO rp_check VALUES(1,10),(4,40)", 2))
		run(fixture.Select("SELECT * FROM rp_check ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
	})
}
