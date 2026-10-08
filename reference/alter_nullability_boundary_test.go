//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, b := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,'seed')", 1))
					tx, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr := func(s referenceStep) {
						s.prepared = prepared
						if s.state != "" {
							s.errorStage = "execute"
							if prepared && s.state == "25P02" {
								s.errorStage = "prepare"
							}
						}
						referenceRunStep(t, p.ctx, tx, b.record, s)
					}
					tr(referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0))
					tr(referenceImplicitError("INSERT INTO t VALUES($1,$2)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad"))
					tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
					tx, err = b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0))
					tr(referenceExec("INSERT INTO t VALUES($1,$2)", 1, 8, "valid"))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					tx, err = b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(referenceExec("ALTER TABLE t DROP CONSTRAINT nn", 0))
					tr(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, "rolled_back"))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(referenceImplicitError("INSERT INTO t VALUES(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}))
					tx, err = b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					tr(referenceExec("ALTER TABLE t DROP CONSTRAINT nn", 0))
					tr(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, "allowed"))
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(referenceSelect("SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "allowed"}, {int64(7), "seed"}, {int64(8), "valid"}}))
				})
			}
		})
	}
}
