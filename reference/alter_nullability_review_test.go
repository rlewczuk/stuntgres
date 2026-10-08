//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityReviewProjectedUpdate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9,11)", 1))
					tx, err := backend.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, tx, backend.record, s) }
					retained, err := tx.PrepareContext(p.ctx, "UPDATE t SET c=$1")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					update := func(n int) {
						t.Helper()
						if prepared {
							r, e := retained.ExecContext(p.ctx, n)
							if e != nil {
								t.Fatal(e)
							}
							count, e := r.RowsAffected()
							if e != nil || count != 1 {
								t.Fatal(count, e)
							}
						} else {
							tr(referenceExec("UPDATE t SET c=$1", 1, n))
						}
					}
					tr(referenceExec("SAVEPOINT before_shape", 0))
					tr(referenceExec("ALTER TABLE t DROP b, ALTER c SET NOT NULL", 0))
					update(12)
					tr(referenceExec("SAVEPOINT middle", 0))
					tr(referenceExec("ALTER TABLE t DROP a", 0))
					update(13)
					tr(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"INT4"}, [][]any{{int64(13)}}))
					tr(referenceExec("ROLLBACK TO middle", 0))
					update(14)
					tr(referenceExec("ROLLBACK TO before_shape", 0))
					update(15)
					tr(referenceExec("ALTER TABLE t DROP a, DROP b, ALTER c SET NOT NULL", 0))
					update(16)
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(referenceSelect("SELECT * FROM t", []string{"c"}, []string{"INT4"}, [][]any{{int64(16)}}))
				})
			}
		})
	}
}

func TestReferenceAlterNullabilityReviewSuccessiveDependencies(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9,11)", 1))
					run(referenceExec("CREATE SEQUENCE owned OWNED BY t.b", 0))
					run(referenceExec("CREATE VIEW v AS SELECT b,c FROM t", 0))
					run(referenceExec("CREATE VIEW w AS SELECT * FROM v", 0))
					if reverse {
						run(referenceImplicitError("ALTER TABLE t DROP c RESTRICT, DROP b CASCADE, ALTER a SET NOT NULL", referenceDiagnostic{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view v depends on column c of table t\nview w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
						run(referenceSelect("SELECT * FROM w", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(11)}}))
					} else {
						run(referenceExec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0))
						run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
				})
			}
		})
	}
}

func TestReferenceAlterNullabilityReviewFirstRRSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b TEXT,c INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,'keep',9)", 1))
					tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, tx, backend.record, s) }
					tr(referenceExec("SAVEPOINT before_shape", 0))
					tr(referenceExec("ALTER TABLE t DROP a, ALTER c SET NOT NULL", 0))
					tr(referenceSelect("SELECT b,c FROM t", []string{"b", "c"}, []string{"TEXT", "INT4"}, [][]any{{"keep", int64(9)}}))
					tr(referenceExec("INSERT INTO t VALUES('own',10)", 1))
					tr(referenceExec("ALTER TABLE t DROP b", 0))
					tr(referenceSelect("SELECT c FROM t ORDER BY c", []string{"c"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
					tr(referenceExec("ROLLBACK TO before_shape", 0))
					tr(referenceExec("INSERT INTO t VALUES(8,'restored',11)", 1))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}, {int64(8), "restored", int64(11)}}))
				})
			}
		})
	}
}
