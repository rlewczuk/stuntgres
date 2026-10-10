package reference_test

import (
	"testing"
	"time"
)

func TestReferenceAlterTypeModifiersAndDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(c TIMESTAMP(6))", 0))
			run(referenceExec("INSERT INTO t VALUES('2024-01-01 00:00:00.123456')", 1))
			run(referencePrepared(referenceExec("ALTER TABLE t ALTER c TYPE TIMESTAMP(2)", 0)))
			run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2024, 1, 1, 0, 0, 0, 120000000, time.UTC)}}))
			run(referenceExec("CREATE TABLE d(c INT DEFAULT (7::integer))", 0))
			run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
			run(referenceExec("ALTER TABLE d ALTER c TYPE BIGINT USING c+1", 0))
			run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
			run(referenceSelect("SELECT c FROM d ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}}))
			run(referenceExec("CREATE TABLE n(c INT)", 0))
			run(referenceExec("INSERT INTO n VALUES(7)", 1))
			run(referenceImplicitError("ALTER TABLE n ADD CONSTRAINT nn NOT NULL c, ALTER c TYPE INT USING NULL", referenceDiagnostic{Code: "23502", Message: `column "c" of relation "n" contains null values`, Schema: "public", Table: "n", Column: "c"}))
			run(referenceSelect("SELECT c FROM n", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("INSERT INTO n VALUES(NULL)", 1))
			run(referenceImplicitError("ALTER TABLE n ALTER c TYPE BIGINT, ALTER missing SET NOT NULL", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "n" does not exist`}))
			run(referenceSelect("SELECT c FROM n ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			run(referenceExec("CREATE TABLE dup(c INT)", 0))
			run(referenceExec("INSERT INTO dup VALUES(7)", 1))
			run(referenceImplicitError("ALTER TABLE dup ALTER c TYPE BIGINT, ALTER c TYPE TEXT", referenceDiagnostic{Code: "0A000", Message: `cannot alter type of column "c" twice`}))
			run(referenceSelect("SELECT c FROM dup", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("ALTER TABLE dup ALTER c TYPE INT, ALTER c TYPE BIGINT", 0))
			run(referenceSelect("SELECT c FROM dup", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceAlterTypeNondefaultLocaleBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE TABLE t(c TEXT)", 0))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(`ALTER TABLE t ALTER c TYPE TEXT COLLATE pg_catalog."C.utf8"`, 0))
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE t(c TEXT)", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceImplicitError(`ALTER TABLE t ALTER c TYPE TEXT COLLATE pg_catalog."C.utf8"`, referenceDiagnostic{Code: "42704", Message: `collation "pg_catalog.C.utf8" for encoding "UTF8" does not exist`, Position: "33"}))
}
