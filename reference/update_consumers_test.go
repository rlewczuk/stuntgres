package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUpdateCoreExplicitFailure(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
			run(b, referenceExec("CREATE TABLE t(id INT,a INT,n SMALLINT NOT NULL)", 0))
			run(b, referenceExec("INSERT INTO t VALUES(1,1,1),(2,40000,1)", 2))
			for _, prepared := range []bool{false, true} {
				tx, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := referenceExec("UPDATE t SET a=7 WHERE id=1", 1)
				step.prepared = prepared
				run(tx, step)
				run(tx, referenceImplicitError("UPDATE t SET n=a", referenceDiagnostic{Code: "22003", Message: "smallint out of range"}))
				run(tx, referenceImplicitError("UPDATE t SET a=8", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(b, referenceSelect("SELECT id,a,n FROM t ORDER BY id", []string{"id", "a", "n"}, []string{"INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(40000), int64(1)}}))
			}
			run(b, referenceExec("UPDATE t SET n=a WHERE id=1", 1))
			run(b, referenceExec("UPDATE t SET a=2 WHERE id=1", 1))
		})
	}
}

func TestReferenceUpdateCoreAlterConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,a INT,b TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')", 3))
			old, err := backend.db.QueryContext(p.ctx, "SELECT id,a,b FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			stmt, err := backend.db.PrepareContext(p.ctx, "UPDATE t SET a=$1 WHERE id=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { old.Close(); stmt.Close() }()
			run(referenceExec("UPDATE t SET a=3 WHERE id=1", 1))
			run(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			run(referenceExec("COMMENT ON COLUMN t.a IS 'current'", 0))
			run(referenceExec("UPDATE t SET a=a", 3))
			run(referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}))
			result, err := stmt.ExecContext(p.ctx, 4, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			run(referenceExec("ALTER TABLE t DROP COLUMN b", 0))
			run(referenceSelect("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
			run(referenceExec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
			result, err = stmt.ExecContext(p.ctx, 5, 1)
			if result != nil {
				t.Fatal("rename returned result")
			}
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "14"})
			updateCoreRetained(t, backend.pg, old, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), nil, "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}, true)
		})
	}
}

func TestReferenceUpdateCoreAlterControls(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	run := func(s referenceStep) { referenceRunStep(t, ctx, p.memory, p.memoryRecord, s) }
	run(referenceExec("CREATE TABLE t(id INT,a INT,b TEXT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')", 3))
	old, err := p.memory.QueryContext(ctx, "SELECT id,a,b FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	stmt, err := p.memory.PrepareContext(ctx, "UPDATE t SET a=$1 WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { old.Close(); stmt.Close() }()
	run(referenceExec("UPDATE t SET a=3 WHERE id=1", 1))
	run(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
	run(referenceExec("COMMENT ON COLUMN t.a IS 'current'", 0))
	run(referenceExec("UPDATE t SET a=a", 3))
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "UPDATE") || strings.Contains(dump.String(), "NULL, 'keep'") {
		t.Fatal("dump serialized history", dump.String())
	}
	if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE bad(v INT); UPDATE bad SET v=1;")); err == nil {
		t.Fatal("Restore admitted UPDATE")
	}
	run(referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}))
	if err := stuntgres.Reset(ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	result, err := stmt.ExecContext(ctx, 4, 1)
	if err != nil {
		t.Fatal("prepared rebind", err)
	}
	referenceLockAffected(t, result, 1)
	run(referenceExec("ALTER TABLE t DROP COLUMN b", 0))
	run(referenceSelect("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
	run(referenceExec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
	_, err = stmt.ExecContext(ctx, 5, 1)
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "14"})
	updateCoreRetained(t, false, old, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), nil, "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}, true)
}
