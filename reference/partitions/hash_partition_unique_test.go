package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionUnique(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_pk(k INT,payload TEXT,CONSTRAINT hp_pk_key PRIMARY KEY(k)) PARTITION BY HASH(k)", 0))
		referenceHashChildren(run, "hp_pk", 4)
		run(fixture.Exec("INSERT INTO hp_pk VALUES(1,'one'),(2,'two'),(17,'seventeen')", 3))
		run(partitionConstraintDuplicate("INSERT INTO hp_pk VALUES(1,'duplicate')", "hp_pk_0", "hp_pk_0_pkey", "Key (k)=(1) already exists."))
		run(fixture.Select("SELECT k,payload FROM hp_pk ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(17), "seventeen"}}))
		run(referenceRangeRows("SELECT k FROM hp_pk_0 ORDER BY k", 1, 17))
		run(referenceRangeRows("SELECT k FROM hp_pk_2", 2))
		run(partitionConstraintIndexState("hp_pk_key", true))
		run(partitionConstraintIndexState("hp_pk_0_pkey", true))
		for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
			query := "CREATE TABLE hp_missing(k INT,payload INT," + kind + "(payload)) PARTITION BY HASH(k)"
			run(partitionConstraintMissing(query, "hp_missing", kind, "k"))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hp_missing'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TYPE hp_missing AS (probe INT)", 0))
			run(fixture.Exec("DROP TYPE hp_missing", 0))
		}
		// An explicit released HASH class must retain compatible B-tree equality.
		run(fixture.Exec("CREATE TABLE hp_explicit(k INT,payload TEXT,UNIQUE(k)) PARTITION BY HASH(k int4_ops)", 0))
		referenceHashChildren(run, "hp_explicit", 8)
		run(fixture.Exec("INSERT INTO hp_explicit VALUES(17,'seventeen'),(42,'answer')", 2))
		run(partitionConstraintDuplicate("INSERT INTO hp_explicit VALUES(17,'duplicate')", "hp_explicit_4", "hp_explicit_4_k_key", "Key (k)=(17) already exists."))
		run(referenceRangeRows("SELECT k FROM hp_explicit_4", 17))
		run(fixture.Exec(`CREATE TABLE hp_pattern(k TEXT COLLATE "C",payload INT,UNIQUE(k)) PARTITION BY HASH(k text_pattern_ops)`, 0))
		referenceHashChildren(run, "hp_pattern", 8)
		run(fixture.Exec("INSERT INTO hp_pattern VALUES('abc',1)", 1))
		run(partitionConstraintDuplicate("INSERT INTO hp_pattern VALUES('abc',2)", "hp_pattern_5", "hp_pattern_5_k_key", "Key (k)=(abc) already exists."))
		run(fixture.Select("SELECT k,payload FROM hp_pattern_5", []string{"k", "payload"}, []string{"TEXT", "INT4"}, [][]any{{"abc", int64(1)}}))
		run(fixture.Exec(`CREATE TABLE hp_collation(k TEXT COLLATE "C") PARTITION BY HASH(k text_pattern_ops)`, 0))
		run(partitionConstraintMissing(`CREATE UNIQUE INDEX hp_bad_collation ON hp_collation(k COLLATE "POSIX")`, "hp_collation", "UNIQUE", "k"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hp_bad_collation'", []string{"relname"}, []string{"NAME"}, nil))
	})
}
