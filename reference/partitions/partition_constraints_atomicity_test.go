package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsAtomicity(t *testing.T) {
	partitionConstraintCase(t, "later_leaf_build_failure", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE pc_b PARTITION OF pc_root FOR VALUES FROM(3) TO(5)", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'a'),(3,'b'),(3,'b')", 3))
		failure := fixture.ErrorDiagnostic("CREATE UNIQUE INDEX pc_new_idx ON pc_root(k,payload)", fixture.ConcurrentIndexUnique("pc_b", "pc_b_k_payload_idx", "Key (k, payload)=(3, b) is duplicated.", true))
		failure.ErrorStage = "execute"
		run(failure)
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_new_idx' OR relname='pc_a_k_payload_idx' OR relname='pc_b_k_payload_idx'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT k,payload FROM pc_root ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "b"}, {int64(3), "b"}}))
		run(fixture.Exec("INSERT INTO pc_a VALUES(2,'healthy')", 1))
		run(fixture.Exec("DROP TABLE pc_b", 0))
		run(fixture.Exec("CREATE TABLE pc_b PARTITION OF pc_root FOR VALUES FROM(3) TO(5)", 0))
		run(fixture.Exec("INSERT INTO pc_b VALUES(3,'b')", 1))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_new_idx ON pc_root(k,payload)", 0))
		run(partitionConstraintIndexState("pc_new_idx", true))
		run(partitionConstraintEdge("pc_a_k_payload_idx", "pc_new_idx", "pc_a"))
		run(partitionConstraintEdge("pc_b_k_payload_idx", "pc_new_idx", "pc_b"))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'a')", "pc_a", "pc_a_k_payload_idx", "Key (k, payload)=(1, a) already exists."))
	})
	partitionConstraintCase(t, "multi_target_late_dependency", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(run)
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
		run(fixture.Exec("CREATE TABLE pc_guard(k INT CONSTRAINT pc_guard_key UNIQUE)", 0))
		run(fixture.Exec("INSERT INTO pc_guard VALUES(1)", 1))
		run(referenceRangeError("DROP INDEX pc_root_idx,pc_guard_key CASCADE", "2BP01", "cannot drop index pc_guard_key because constraint pc_guard_key on table pc_guard requires it", fixture.Diagnostic{Hint: "You can drop constraint pc_guard_key on table pc_guard instead."}))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_a_idx", "Key (k, payload)=(1, sentinel) already exists."))
		run(partitionConstraintDuplicate("INSERT INTO pc_guard VALUES(1)", "pc_guard", "pc_guard_key", "Key (k)=(1) already exists."))
		run(fixture.Exec("DROP INDEX pc_root_idx", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_guard VALUES(1)", "pc_guard", "pc_guard_key", "Key (k)=(1) already exists."))
	})
}
