package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Predicted from ordinary DETACH's edge removal (not ATTACH completion).
// The root must settle these independently specified flags on pinned18 first.
func partitionDetachInvalidSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pd_invalid(k INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_indexed PARTITION OF pd_invalid FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pd_unindexed PARTITION OF pd_invalid FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("INSERT INTO pd_invalid VALUES(1),(3)", 2))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_invalid_idx ON ONLY pd_invalid(k)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_indexed_idx ON pd_indexed(k)", 0))
	run(fixture.Exec("ALTER INDEX pd_invalid_idx ATTACH PARTITION pd_indexed_idx", 0))
	run(partitionConstraintIndexState("pd_invalid_idx", false))
	run(partitionConstraintEdge("pd_indexed_idx", "pd_invalid_idx", "pd_indexed"))
}

func partitionDetachInvalidFinal(run func(fixture.Step)) {
	run(partitionConstraintIndexState("pd_invalid_idx", false))
	run(partitionConstraintIndexState("pd_indexed_idx", true))
	run(partitionDetachClass("pd_indexed_idx", false))
	run(partitionDetachTopEdges("pd_indexed_idx", false))
	run(referenceRangeRows("SELECT k FROM pd_indexed", 1))
	run(referenceRangeRows("SELECT k FROM pd_unindexed", 3))
	run(fixture.Select("SELECT k FROM pd_invalid", []string{"k"}, []string{"INT4"}, nil))
}

func partitionDetachNestedInvalidSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pd_ix_root(k INT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_ix_mid PARTITION OF pd_ix_root DEFAULT PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_ix_a PARTITION OF pd_ix_mid FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pd_ix_b PARTITION OF pd_ix_mid DEFAULT", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_ix_root_idx ON ONLY pd_ix_root(k)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_ix_mid_idx ON ONLY pd_ix_mid(k)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pd_ix_a_idx ON pd_ix_a(k)", 0))
	run(fixture.Exec("ALTER INDEX pd_ix_mid_idx ATTACH PARTITION pd_ix_a_idx", 0))
	run(fixture.Exec("ALTER INDEX pd_ix_root_idx ATTACH PARTITION pd_ix_mid_idx", 0))
	run(partitionConstraintIndexState("pd_ix_root_idx", false))
	run(partitionConstraintIndexState("pd_ix_mid_idx", false))
}

func partitionDetachNestedInvalidFinal(run func(fixture.Step)) {
	run(partitionDetachTopEdges("pd_ix_mid_idx", false))
	run(partitionConstraintEdge("pd_ix_a_idx", "pd_ix_mid_idx", "pd_ix_a"))
	run(partitionConstraintIndexState("pd_ix_mid_idx", false))
	run(partitionConstraintIndexState("pd_ix_root_idx", false))
	run(partitionDetachClass("pd_ix_mid", false))
	run(partitionDetachClass("pd_ix_a", true))
}

func TestReferencePartitionDetachIndexes(t *testing.T) {
	t.Run("invalid_only", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			partitionDetachInvalidSetup(run)
			run(fixture.Exec("ALTER TABLE pd_invalid DETACH PARTITION pd_unindexed", 0))
			run(partitionConstraintIndexState("pd_invalid_idx", false))
			run(partitionConstraintEdge("pd_indexed_idx", "pd_invalid_idx", "pd_indexed"))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE pd_invalid DETACH PARTITION pd_indexed", 0)))
			partitionDetachInvalidFinal(run)
		})
	})
	t.Run("nested_incomplete", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			partitionDetachNestedInvalidSetup(run)
			run(fixture.Exec("ALTER TABLE pd_ix_mid DETACH PARTITION pd_ix_b", 0))
			run(partitionConstraintIndexState("pd_ix_mid_idx", false))
			run(partitionConstraintIndexState("pd_ix_root_idx", false))
			run(fixture.Exec("ALTER TABLE pd_ix_root DETACH PARTITION pd_ix_mid", 0))
			partitionDetachNestedInvalidFinal(run)
		})
	})
	for _, kind := range []string{"plain", "unique", "primary"} {
		t.Run("complete/"+kind, func(t *testing.T) {
			partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				decl := ""
				if kind == "unique" {
					decl = ",CONSTRAINT pd_complete_idx UNIQUE(k)"
				}
				if kind == "primary" {
					decl = ",CONSTRAINT pd_complete_idx PRIMARY KEY(k)"
				}
				run(fixture.Exec("CREATE TABLE pd_complete(k INT"+decl+") PARTITION BY RANGE(k)", 0))
				if kind == "plain" {
					run(fixture.Exec("CREATE UNIQUE INDEX pd_complete_idx ON pd_complete(k)", 0))
				}
				run(fixture.Exec("CREATE TABLE pd_complete_a PARTITION OF pd_complete FOR VALUES FROM(1) TO(3)", 0))
				run(fixture.Exec("CREATE TABLE pd_complete_b PARTITION OF pd_complete DEFAULT", 0))
				index := "pd_complete_a_k_key"
				if kind == "plain" {
					index = "pd_complete_a_k_idx"
				}
				if kind == "primary" {
					index = "pd_complete_a_pkey"
				}
				identity := partitionDetachCapture(t, p, b.DB, "SELECT oid::BIGINT,relowner::BIGINT FROM pg_class WHERE relname='"+index+"'")
				run(partitionConstraintEdge(index, "pd_complete_idx", "pd_complete_a"))
				run(fixture.Exec("ALTER TABLE pd_complete DETACH PARTITION pd_complete_a", 0))
				run(partitionConstraintIndexState("pd_complete_idx", true))
				run(partitionConstraintIndexState(index, true))
				run(partitionDetachTopEdges(index, false))
				run(fixture.Select("SELECT oid::BIGINT,relowner::BIGINT FROM pg_class WHERE relname='"+index+"'", []string{"oid", "relowner"}, []string{"INT8", "INT8"}, identity))
				if kind == "plain" {
					run(fixture.Exec("DROP INDEX pd_complete_idx", 0))
				} else {
					run(fixture.Exec("ALTER TABLE pd_complete DROP CONSTRAINT pd_complete_idx", 0))
				}
				run(partitionConstraintIndexState(index, true))
				if kind != "plain" {
					run(fixture.Exec("ALTER TABLE pd_complete_a DROP CONSTRAINT "+index, 0))
				}
				run(fixture.Exec("ALTER TABLE pd_complete_a DROP COLUMN k", 0))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='"+index+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(partitionDetachClass("pd_complete_b", true))
			})
		})
	}
	t.Run("attach_completion_control", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			partitionDetachInvalidSetup(run)
			run(fixture.Exec("CREATE UNIQUE INDEX pd_unindexed_idx ON pd_unindexed(k)", 0))
			run(fixture.Exec("ALTER INDEX pd_invalid_idx ATTACH PARTITION pd_unindexed_idx", 0))
			run(partitionConstraintIndexState("pd_invalid_idx", true))
			run(fixture.Exec("ALTER TABLE pd_invalid DETACH PARTITION pd_indexed", 0))
			run(partitionConstraintIndexState("pd_invalid_idx", true))
		})
	})
}
