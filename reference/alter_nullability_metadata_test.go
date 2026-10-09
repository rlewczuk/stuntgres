package reference_test

import (
	"context"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(NULL,'old')", 1))
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
				s.prepared = prepared
				s.errorStage = "execute"
				p.step(t, s)
			}

		})
	}
}

func TestReferenceAlterNullabilityNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(a INT,b INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(7,9)", 1))
			step := referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ADD CONSTRAINT other NOT NULL a", 0)
			step.prepared = prepared
			p.step(t, step)
			p.step(t, referenceImplicitError("ALTER TABLE t DROP CONSTRAINT other", referenceDiagnostic{Code: "42704", Message: `constraint "other" of relation "t" does not exist`}))
			p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT nn, ADD CONSTRAINT nn NOT NULL a", 0))
			step = referenceExec("ALTER TABLE t DROP CONSTRAINT nn CASCADE, ADD CONSTRAINT nn NOT NULL b", 0)
			step.prepared = prepared
			p.step(t, step)
			p.step(t, referenceExec("ALTER TABLE t ALTER a SET NOT NULL, ADD CONSTRAINT nn NOT NULL a", 0))
			// SET supplies a compatible identity before the transformed ADD pass.
			p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
			p.step(t, referenceImplicitError("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", referenceDiagnostic{Code: "42710", Message: `constraint "nn" for relation "t" already exists`}))
			step = referenceExec("INSERT INTO t VALUES(NULL,10)", 1)
			step.prepared = prepared
			p.step(t, step)
			p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT nn RESTRICT", 0))
		})
	}
}

func TestReferenceAlterNullabilityPreparedMetadata(t *testing.T) {
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
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
			selectStmt, err := tx.PrepareContext(p.ctx, "SELECT * FROM t ORDER BY b")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			referenceNotNullRetainedRows(t, context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), selectStmt, !b.pg, true, [][]any{{int64(7), "seed"}})
			utilityQuery := "ALTER TABLE t ALTER a SET NOT NULL"
			utility, err := tx.PrepareContext(p.ctx, utilityQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer utility.Close()
			heldUtility := referenceHeldStatement{referenceExecutor: tx, statement: utility, query: utilityQuery}
			referenceRunStep(t, p.ctx, heldUtility, b.recorder, referenceExec(utilityQuery, 0))
			referenceNotNullRetainedRows(t, context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), selectStmt, !b.pg, false, [][]any{{int64(7), "seed"}})
			tr(referenceExec("SAVEPOINT child", 0))
			tr(referenceExec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(NULL,'child')", 1))
			referenceRunStep(t, p.ctx, heldUtility, b.recorder, referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			tr(referenceExec("ROLLBACK TO child", 0))
			referenceNotNullRetainedRows(t, context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), selectStmt, !b.pg, false, [][]any{{int64(7), "seed"}})
			tr(referenceExec("RELEASE child", 0))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, false, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{true, true}, 1)
			run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
			run(referenceExec("INSERT INTO t VALUES(NULL,'after')", 1))
		})
	}
}

func TestReferenceAlterNullabilityOwnDeletesAndSequences(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL,1),(7,2)", 2))
			run(referenceExec("CREATE SEQUENCE seq START 10", 0))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
			tr(referenceExec("DELETE FROM t WHERE b=1", 1))
			tr(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(referenceExec("ALTER SEQUENCE seq RESTART WITH 20", 0))
			tr(referenceExec("SAVEPOINT child", 0))
			tr(referenceExec("ALTER TABLE t DROP b", 0))
			tr(referenceExec("INSERT INTO t VALUES(8)", 1))
			tr(referenceExec("ROLLBACK TO child", 0))
			tr(referenceExec("INSERT INTO t VALUES(9,3)", 1))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(2)}, {int64(9), int64(3)}}))
			run(referenceSelect("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}))
			tx, err = b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr(referenceExec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(NULL,4)", 1))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceImplicitError("INSERT INTO t VALUES(NULL,4)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 4).", Schema: "public", Table: "t", Column: "a"}))
			tx, err = b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr(referenceExec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(NULL,4)", 1))
			tr(referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
