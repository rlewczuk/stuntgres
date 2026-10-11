package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterTypeModifiersAndDefaults(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c TIMESTAMP(6))", 0))
			run(fixture.Exec("INSERT INTO t VALUES('2024-01-01 00:00:00.123456')", 1))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER c TYPE TIMESTAMP(2)", 0)))
			run(fixture.Select("SELECT c FROM t", []string{"c"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2024, 1, 1, 0, 0, 0, 120000000, time.UTC)}}))
			run(fixture.Exec("CREATE TABLE d(c INT DEFAULT (7::integer))", 0))
			run(fixture.Exec("INSERT INTO d DEFAULT VALUES", 1))
			run(fixture.Exec("ALTER TABLE d ALTER c TYPE BIGINT USING c+1", 0))
			run(fixture.Exec("INSERT INTO d DEFAULT VALUES", 1))
			run(fixture.Select("SELECT c FROM d ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}}))
			run(fixture.Exec("CREATE TABLE n(c INT)", 0))
			run(fixture.Exec("INSERT INTO n VALUES(7)", 1))
			run(fixture.ImplicitError("ALTER TABLE n ADD CONSTRAINT nn NOT NULL c, ALTER c TYPE INT USING NULL", fixture.Diagnostic{Code: "23502", Message: `column "c" of relation "n" contains null values`, Schema: "public", Table: "n", Column: "c"}))
			run(fixture.Select("SELECT c FROM n", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("INSERT INTO n VALUES(NULL)", 1))
			run(fixture.ImplicitError("ALTER TABLE n ALTER c TYPE BIGINT, ALTER missing SET NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "n" does not exist`}))
			run(fixture.Select("SELECT c FROM n ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			run(fixture.Exec("CREATE TABLE dup(c INT)", 0))
			run(fixture.Exec("INSERT INTO dup VALUES(7)", 1))
			run(fixture.ImplicitError("ALTER TABLE dup ALTER c TYPE BIGINT, ALTER c TYPE TEXT", fixture.Diagnostic{Code: "0A000", Message: `cannot alter type of column "c" twice`}))
			run(fixture.Select("SELECT c FROM dup", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("ALTER TABLE dup ALTER c TYPE INT, ALTER c TYPE BIGINT", 0))
			run(fixture.Select("SELECT c FROM dup", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceAlterTypeNondefaultLocaleBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("CREATE TABLE t(c TEXT)", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`ALTER TABLE t ALTER c TYPE TEXT COLLATE pg_catalog."C.utf8"`, 0))
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE t(c TEXT)", 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.ImplicitError(`ALTER TABLE t ALTER c TYPE TEXT COLLATE pg_catalog."C.utf8"`, fixture.Diagnostic{Code: "42704", Message: `collation "pg_catalog.C.utf8" for encoding "UTF8" does not exist`, Position: "33"}))
}
