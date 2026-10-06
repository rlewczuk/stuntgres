//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceAlterRenamePrepared(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"memory", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			record := &referenceRecorder{}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,'keep')", 1))
			prepare := func(q string) *sql.Stmt {
				s, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(q, err)
				}
				deferCleanup := s
				t.Cleanup(func() { deferCleanup.Close() })
				return s
			}
			star, named, other := prepare("SELECT * FROM t"), prepare("SELECT a FROM t"), prepare("SELECT b FROM t")
			target, ordered := prepare("INSERT INTO t(a,b) VALUES($1,$2)"), prepare("INSERT INTO t VALUES($1,$2)")
			run(referencePrepared(referenceExec("ALTER TABLE t RENAME a TO renamed", 0)))
			_, err = star.QueryContext(p.ctx)
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
			// pq exposes only PG_DIAG_CONTEXT, not the C Routine source field.
			referenceAssertDiagnostic(t, err, &want)
			_, err = named.QueryContext(p.ctx)
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" does not exist`, Position: "8"}
			referenceAssertDiagnostic(t, err, &want)
			_, err = target.ExecContext(p.ctx, 8, "bad")
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "15"}
			referenceAssertDiagnostic(t, err, &want)
			rows, err := other.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			var b string
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err = rows.Scan(&b); err != nil || b != "keep" {
				t.Fatal(b, err)
			}
			if rows.Next() {
				t.Fatal("extra row")
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			result, err := ordered.ExecContext(p.ctx, 8, "tail")
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			run(referencePrepared(referenceExec("INSERT INTO t(renamed,b) VALUES($1,$2)", 1, 9, "fresh")))
			run(referencePrepared(referenceSelect("SELECT * FROM t ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(8), "tail"}, {int64(9), "fresh"}})))
			run(referencePrepared(referenceExec("ALTER TABLE t RENAME TO newtable", 0)))
			_, err = other.QueryContext(p.ctx)
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}
			referenceAssertDiagnostic(t, err, &want)
			// Utility preparation does not bind its source; creation after Prepare is visible at Exec.
			late := prepare("ALTER TABLE late RENAME TO later")
			run(referenceExec("CREATE TABLE late(a INT)", 0))
			result, err = late.ExecContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 0)
			observed := runSelectRename(t, p, c, record)
			if !reflect.DeepEqual(observed.columns, []string{"renamed", "b"}) {
				t.Fatal(observed)
			}
		})
	}
}

func runSelectRename(t *testing.T, p *referencePair, c *sql.Conn, record *referenceRecorder) referenceObserved {
	return referenceRunStep(t, p.ctx, c, record, referenceSelect("SELECT * FROM newtable ORDER BY renamed", []string{"renamed", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}, {int64(8), "tail"}, {int64(9), "fresh"}}))
}
