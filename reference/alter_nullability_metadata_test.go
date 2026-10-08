//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityValidation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(NULL,'old')", 1))
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"})
				s.prepared = prepared
				s.errorStage = "execute"
				p.step(t, s)
			}
			p.step(t, referencePrepared(referenceExec("UPDATE t SET a=7", 1)))
			s := referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ALTER b SET NOT NULL", 0)
			s.prepared = prepared
			p.step(t, s)
			for _, q := range []string{"INSERT INTO t VALUES($1,$2)", "UPDATE t SET a=$1,b=$2"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}, nil, "bad")
				s.prepared = true
				s.errorStage = "execute"
				p.step(t, s)
			}
			p.step(t, referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "old"}}))
			p.step(t, referencePrepared(referenceExec("ALTER TABLE t DROP CONSTRAINT nn, ALTER b DROP NOT NULL", 0)))
			p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, "after")))
			p.step(t, referenceExec("DELETE FROM t WHERE b='after'", 1))
			p.step(t, referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			p.step(t, referenceImplicitError("INSERT INTO t SELECT NULL,'query'", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, query).", Schema: "public", Table: "t", Column: "a"}))
		})
	}
}

func TestReferenceAlterNullabilityNames(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7,9)", 1))
	p.step(t, referenceExec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a, ADD CONSTRAINT other NOT NULL a", 0))
	p.step(t, referenceImplicitError("ALTER TABLE t DROP CONSTRAINT other", referenceDiagnostic{Code: "42704", Message: `constraint "other" of relation "t" does not exist`}))
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT nn, ADD CONSTRAINT nn NOT NULL a", 0))
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT nn CASCADE, ADD CONSTRAINT nn NOT NULL b", 0))
	p.step(t, referenceExec("ALTER TABLE t ALTER a SET NOT NULL, ADD CONSTRAINT nn NOT NULL a", 0))
	// SET supplies a compatible identity before the transformed ADD pass.
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT t_a_not_null", 0))
	p.step(t, referenceImplicitError("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", referenceDiagnostic{Code: "42710", Message: `constraint "nn" for relation "t" already exists`}))
	p.step(t, referenceExec("INSERT INTO t VALUES(NULL,10)", 1))
	p.step(t, referenceExec("ALTER TABLE t DROP CONSTRAINT nn RESTRICT", 0))
}

func TestReferenceAlterNullabilityPreparedMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
		native bool
	}{{"postgres", p.postgres, p.postgresRecord, false}, {"native", p.memory, p.memoryRecord, true}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,'seed')", 1))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.record, s) }
			selectStmt, err := tx.PrepareContext(p.ctx, "SELECT * FROM t ORDER BY b")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			referenceNotNullRetainedRows(t, p.ctx, selectStmt, b.native, true, [][]any{{int64(7), "seed"}})
			tr(referencePrepared(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0)))
			referenceNotNullRetainedRows(t, p.ctx, selectStmt, b.native, false, [][]any{{int64(7), "seed"}})
			tr(referenceExec("SAVEPOINT child", 0))
			tr(referenceExec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
			tr(referenceExec("INSERT INTO t VALUES(NULL,'child')", 1))
			tr(referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}))
			tr(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			tr(referenceExec("ROLLBACK TO child", 0))
			referenceNotNullRetainedRows(t, p.ctx, selectStmt, b.native, false, [][]any{{int64(7), "seed"}})
			tr(referenceExec("RELEASE child", 0))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("INSERT INTO t VALUES(NULL,'after')", 1))
		})
	}
}

func TestReferenceAlterNullabilityOwnDeletesAndSequences(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL,1),(7,2)", 2))
			run(referenceExec("CREATE SEQUENCE seq START 10", 0))
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.record, s) }
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
		})
	}
}

func TestReferenceAlterNullabilityAuthorizationAndPhase(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
	for _, b := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
		tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		s, err := tx.PrepareContext(p.ctx, "ALTER TABLE absent ALTER missing SET NOT NULL, DROP b")
		if err != nil {
			t.Fatal("effect-free prepare", err)
		}
		_, err = s.ExecContext(p.ctx)
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}
		referenceAssertDiagnostic(t, err, &want)
		s.Close()
		tx.Rollback()
	}
}
