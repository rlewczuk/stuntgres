package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsCompoundAdmission(t *testing.T) {
	for _, probe := range []struct{ name, query, code, message, position string }{
		{"type_before_missing_key", "CREATE TABLE pc_bad(a pc_missing_type,UNIQUE(missing)) PARTITION BY RANGE(a)", "42704", `type "pc_missing_type" does not exist`, "23"},
		{"binding_before_coverage", "CREATE UNIQUE INDEX pc_bad ON pc_root(payload,missing)", "42703", `column "missing" does not exist`, ""},
	} {
		partitionConstraintCase(t, probe.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_root DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
			run(referenceRangeError(probe.query, probe.code, probe.message, fixture.Diagnostic{Position: probe.position}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_bad'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
			run(fixture.Exec("CREATE UNIQUE INDEX pc_bad ON pc_root(k,payload)", 0))
			run(partitionConstraintIndexState("pc_bad", true))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_leaf", "pc_leaf_k_payload_idx", "Key (k, payload)=(1, sentinel) already exists."))
			run(fixture.Exec("INSERT INTO pc_root VALUES(2,'healthy')", 1))
		})
	}

	partitionConstraintCase(t, "bounds_before_inherited_coverage", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT,UNIQUE(k)) PARTITION BY RANGE(k)", 0))
		query := "CREATE TABLE pc_bad PARTITION OF pc_root FOR VALUES FROM('bad') TO(3) PARTITION BY RANGE(payload)"
		run(referenceRangeError(query, "22P02", `invalid input syntax for type integer: "bad"`, fixture.Diagnostic{Position: referenceRangePosition(query, "'bad'")}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_bad' OR relname='pc_bad_k_key'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE TABLE pc_bad PARTITION OF pc_root FOR VALUES FROM(1) TO(3) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_bad DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'healthy')", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_bad VALUES(1,'duplicate')", "pc_leaf", "pc_leaf_k_key", "Key (k)=(1) already exists."))
	})
}
