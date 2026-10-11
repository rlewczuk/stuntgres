package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCurrentOfUsingReturningAndConstraints(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL DEFAULT 9,v TEXT,CONSTRAINT positive CHECK(id>0),CONSTRAINT key UNIQUE(id))", 0))
				run(fixture.Exec("CREATE TABLE s(label TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,NULL),(3,'three')", 3))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=2 FOR UPDATE", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
				q := "DELETE FROM t AS d USING s AS x WHERE CURRENT OF c RETURNING WITH (OLD AS o, NEW AS n) d.id,o.v,n.id,x.label,TRUE AS flag"
				s := fixture.Select(q, []string{"id", "v", "id", "label", "flag"}, []string{"INT4", "TEXT", "INT4", "TEXT", "BOOL"}, nil)
				s.Prepared = prepared
				run(s)
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
				run(fixture.Exec("INSERT INTO s VALUES('same'),('same')", 2))
				run(fixture.Exec("SAVEPOINT deleted", 0))
				s.Rows = [][]any{{int64(2), nil, nil, "same", true}}
				run(s)
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3)
				run(fixture.Exec("INSERT INTO t VALUES(2,'replacement')", 1))
				run(fixture.Exec("ROLLBACK TO deleted", 0))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
				run(s)
				run(fixture.Exec("COMMIT", 0))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3)
				run(fixture.Exec("INSERT INTO t(v) VALUES('default')", 1))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3, 9)
				// Existing constraint owners remain authoritative after positioned deletion.
				for _, tc := range []struct {
					q string
					d fixture.Diagnostic
				}{
					{"INSERT INTO t VALUES(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "id"}},
					{"INSERT INTO t VALUES(0,'bad')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Detail: "Failing row contains (0, bad).", Schema: "public", Table: "t", Constraint: "positive"}},
					{"INSERT INTO t VALUES(1,'bad')", fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "key"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "key"}},
				} {
					tc.d.Severity = "ERROR"
					tc.d.SeverityUnlocalized = "ERROR"
					updateFromDiagnostic(t, p.Ctx, c, tc.q, tc.d, false)
				}
				run(fixture.Exec("INSERT INTO t VALUES(2,'reused')", 1))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3, 9)
			})
		})
	}
	t.Run("empty_source_lookup_precedence", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("CREATE TABLE s(id INT)", 0))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			// PostgreSQL 18.0 validates the current cursor even when USING is empty;
			// a missing cursor remains an error rather than a successful zero count.
			currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t USING s WHERE CURRENT OF missing", "34000", `cursor "missing" does not exist`, false))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
		})
	})
	t.Run("absent_outer_mark", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			run(fixture.Exec("CREATE TABLE s(id INT)", 0))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			// Mark the preserved target side: a matched target is independently eligible
			// even though the unmarked outer side has no physical occurrence.
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT t.id FROM t LEFT JOIN s ON t.id=s.id WHERE t.id=2 FOR UPDATE OF t", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 2))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 3)
		})
	})
	t.Run("postgres_foreign_key", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE child(id INT REFERENCES t(id))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t FOR UPDATE", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			s := fixture.ImplicitError("DELETE FROM t WHERE CURRENT OF c", fixture.Diagnostic{Code: "23503", Message: `update or delete on table "t" violates foreign key constraint "child_id_fkey" on table "child"`, Detail: `Key (id)=(1) is still referenced from table "child".`, Schema: "public", Table: "child", Constraint: "child_id_fkey"})
			s.ErrorStage = "execute"
			currentOfFailure(t, p, b, c, s)
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1)
			run(fixture.Exec("DELETE FROM child", 1))
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
		})
	})
}
