package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
						referenceRunStep(t, p.ctx, tx, b.recorder, s)
					}
					tr(referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0))
					tr(referenceImplicitError("INSERT INTO t VALUES($1,$2)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad"))
					tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
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
					referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 2)
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
					referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, false}, 3)
					run(referenceSelect("SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "allowed"}, {int64(7), "seed"}, {int64(8), "valid"}}))
				})
			}
		})
	}
}
