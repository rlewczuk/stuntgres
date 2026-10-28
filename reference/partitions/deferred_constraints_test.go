package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintPropagation(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(kind, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "deferred_moved")
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u "+kind+"(v) INITIALLY DEFERRED) PARTITION BY RANGE(v)", 0))
				run(fixture.Exec("CREATE TABLE leaf PARTITION OF t FOR VALUES FROM(0) TO(10)", 0))
				run(fixture.Select("SELECT c.condeferrable,c.condeferred,i.indimmediate FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid WHERE c.conrelid IN ('t'::regclass,'leaf'::regclass) AND c.contype IN ('u','p') ORDER BY c.conrelid", []string{"condeferrable", "condeferred", "indimmediate"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}, {true, true, false}}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT repair", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(1)", 2))
				run(fixture.Exec("SAVEPOINT s", 0))
				d := fixture.DeferredUniqueConflict("leaf", "leaf_v_key", "v", "1")
				if kind == "PRIMARY KEY" {
					d = fixture.DeferredUniqueConflict("leaf", "leaf_pkey", "v", "1")
				}
				run(fixture.ErrorDiagnostic("SET CONSTRAINTS u IMMEDIATE", d))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.Exec("ROLLBACK TO repair", 0))
				run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
				run(fixture.Exec("SET CONSTRAINTS u IMMEDIATE", 0))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Exec("CREATE TABLE copied(LIKE leaf INCLUDING INDEXES)", 0))
				run(fixture.Select("SELECT condeferrable,condeferred FROM pg_constraint WHERE conrelid='copied'::regclass AND contype IN ('u','p')", []string{"condeferrable", "condeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
				run(fixture.Exec("ALTER TABLE copied ALTER v TYPE BIGINT", 0))
				run(fixture.Exec("CREATE SCHEMA deferred_moved", 0))
				run(fixture.Exec("ALTER TABLE copied SET SCHEMA deferred_moved", 0))
				run(fixture.Exec("ALTER TABLE deferred_moved.copied RENAME TO renamed", 0))
				run(fixture.Select("SELECT condeferrable,condeferred FROM pg_constraint WHERE conrelid='deferred_moved.renamed'::regclass AND contype IN ('u','p')", []string{"condeferrable", "condeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Prepared(fixture.Exec("INSERT INTO deferred_moved.renamed VALUES($1),($1)", 2, int64(1))))
				run(fixture.Exec("DELETE FROM deferred_moved.renamed", 2))
				run(fixture.Exec("COMMIT", 0))
			})
		})
	}
}
