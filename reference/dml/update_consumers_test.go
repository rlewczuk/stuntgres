package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreExplicitFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
			run(b, fixture.Exec("CREATE TABLE t(id INT,a INT,n SMALLINT NOT NULL)", 0))
			run(b, fixture.Exec("INSERT INTO t VALUES(1,1,1),(2,40000,1)", 2))
			for _, prepared := range []bool{false, true} {
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := fixture.Exec("UPDATE t SET a=7 WHERE id=1", 1)
				step.Prepared = prepared
				run(tx, step)
				run(tx, fixture.ImplicitError("UPDATE t SET n=a", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"}))
				run(tx, fixture.ImplicitError("UPDATE t SET a=8", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(b, fixture.Select("SELECT id,a,n FROM t ORDER BY id", []string{"id", "a", "n"}, []string{"INT4", "INT4", "INT2"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(40000), int64(1)}}))
			}
			run(b, fixture.Exec("UPDATE t SET n=a WHERE id=1", 1))
			run(b, fixture.Exec("UPDATE t SET a=2 WHERE id=1", 1))
		})
	}
}

func TestReferenceUpdateCoreAlterConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,a INT,b TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')", 3))
			old, err := backend.DB.QueryContext(p.Ctx, "SELECT id,a,b FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			stmt, err := backend.DB.PrepareContext(p.Ctx, "UPDATE t SET a=$1 WHERE id=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { old.Close(); stmt.Close() }()
			run(fixture.Exec("UPDATE t SET a=3 WHERE id=1", 1))
			run(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			run(fixture.Exec("COMMENT ON COLUMN t.a IS 'current'", 0))
			run(fixture.Exec("UPDATE t SET a=a", 3))
			run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}))
			result, err := stmt.ExecContext(p.Ctx, 4, 1)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			run(fixture.Exec("ALTER TABLE t DROP COLUMN b", 0))
			run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
			run(fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
			result, err = stmt.ExecContext(p.Ctx, 5, 1)
			if result != nil {
				t.Fatal("rename returned result")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "14"})
			fixture.UpdateCoreRetained(t, backend.PG, old, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), nil, "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}, true)
		})
	}
}

func TestReferenceUpdateCoreAlterControls(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	run := func(s fixture.Step) { fixture.RunStep(t, ctx, p.Memory, p.MemoryRecord, s) }
	run(fixture.Exec("CREATE TABLE t(id INT,a INT,b TEXT)", 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,NULL,'keep'),(2,2,'other'),(2,2,'other')", 3))
	old, err := p.Memory.QueryContext(ctx, "SELECT id,a,b FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	stmt, err := p.Memory.PrepareContext(ctx, "UPDATE t SET a=$1 WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { old.Close(); stmt.Close() }()
	run(fixture.Exec("UPDATE t SET a=3 WHERE id=1", 1))
	run(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
	run(fixture.Exec("COMMENT ON COLUMN t.a IS 'current'", 0))
	run(fixture.Exec("UPDATE t SET a=a", 3))
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "UPDATE") || strings.Contains(dump.String(), "NULL, 'keep'") {
		t.Fatal("dump serialized history", dump.String())
	}
	if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE bad(v INT); UPDATE bad SET v=1;")); err == nil {
		t.Fatal("Restore admitted UPDATE")
	}
	run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(3), "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}))
	if err := stuntgres.Reset(ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	result, err := stmt.ExecContext(ctx, 4, 1)
	if err != nil {
		t.Fatal("prepared rebind", err)
	}
	fixture.LockAffected(t, result, 1)
	run(fixture.Exec("ALTER TABLE t DROP COLUMN b", 0))
	run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
	run(fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
	_, err = stmt.ExecContext(ctx, 5, 1)
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "a" of relation "t" does not exist`, Position: "14"})
	fixture.UpdateCoreRetained(t, false, old, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), nil, "keep"}, {int64(2), int64(2), "other"}, {int64(2), int64(2), "other"}}, true)
}
