package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictUpdateHeldLifecycle(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT,v INT)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX ui ON t(k)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		q := "INSERT INTO t VALUES($1,$2) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), int64(1)))
		run(fixture.Exec("DROP INDEX ui", 0))
		s := insertConflictNoMatch(q)
		s.Args = []any{int64(1), int64(2)}
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("CREATE UNIQUE INDEX replacement ON t(k DESC)", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), int64(2)))
		run(fixture.Exec("ALTER INDEX replacement RENAME TO current_ui", 0))
		run(fixture.Exec("ALTER TABLE t ALTER k TYPE BIGINT", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), int64(3)))
		run(fixture.Exec("ALTER TABLE t RENAME TO renamed", 0))
		run(fixture.Exec("ALTER TABLE renamed RENAME TO t", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), int64(4)))
		run(fixture.Select("SELECT k,v FROM t", []string{"k", "v"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(20)}}))
		run(fixture.Exec("DROP TABLE t", 0))
		run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,100)", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), int64(5)))
		run(conflictUpdateRows([]any{int64(1), int64(105)}))
	})
}
func TestReferenceInsertConflictUpdateControls(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v TEXT,active BOOL,p TEXT)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX CONCURRENTLY expr ON t(lower(v) COLLATE \"C\" text_ops) INCLUDE(p)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX subset_index ON t(p) WHERE active", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'A',TRUE,'old')", 1))
		q := "INSERT INTO t AS dst VALUES($1,$2,TRUE,$3) ON CONFLICT ON CONSTRAINT u DO UPDATE SET v=excluded.v,p=excluded.p"
		stmt, e := b.DB.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), "B", "updated"))
		run(fixture.Exec("INSERT INTO t VALUES(2,'C',FALSE,'new') ON CONFLICT(k) DO UPDATE SET p=excluded.p", 1))
		if !b.PG {
			retained, e := b.DB.QueryContext(p.Ctx, "SELECT k,p FROM t ORDER BY k")
			if e != nil {
				t.Fatal(e)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			for _, def := range []string{"UNIQUE", "lower", "INCLUDE", "WHERE"} {
				if !strings.Contains(dump.String(), def) {
					t.Fatal("missing provider", def, dump.String())
				}
			}
			if strings.Contains(dump.String(), "ON CONFLICT") {
				t.Fatal("Dump serialized action plan")
			}
			if e := stuntgres.Reset(p.Ctx, p.Native); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
				t.Fatal(e)
			}
			cols, e := retained.Columns()
			if e != nil || len(cols) != 2 || cols[0] != "k" || cols[1] != "p" {
				t.Fatal(cols, e)
			}
			types, e := retained.ColumnTypes()
			if e != nil || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal(types, e)
			}
			for _, want := range []struct {
				k int64
				p string
			}{{1, "updated"}, {2, "new"}} {
				if !retained.Next() {
					t.Fatal("retained row lost", retained.Err())
				}
				var k, payload any
				if e := retained.Scan(&k, &payload); e != nil || k != want.k || payload != want.p {
					t.Fatal(k, payload, e)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained tail", retained.Err())
			}
			if e := retained.Close(); e != nil {
				t.Fatal(e)
			}
			for _, bad := range []string{
				"CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1) ON CONFLICT(k) DO UPDATE SET k=excluded.k;",
				"CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1),(1);",
				"CREATE TABLE replacement(k INT UNIQUE); INSERT INTO replacement VALUES(1); broken;",
			} {
				e := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				if e == nil {
					t.Fatal("invalid Restore succeeded", bad)
				}
				if strings.Contains(bad, "ON CONFLICT") {
					d := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT conflict clauses are unsupported")
					fixture.AssertDiagnostic(t, e, &d)
				}
				var preserved bytes.Buffer
				if e := stuntgres.Dump(p.Ctx, p.Native, &preserved); e != nil || preserved.String() != dump.String() {
					t.Fatal("failed Restore changed state", e, preserved.String())
				}
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			e = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			failure := errors.New("conflict update restore reader failure")
			if e := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure}); !errors.Is(e, failure) {
				t.Fatal(e)
			}
			var preserved bytes.Buffer
			if e := stuntgres.Dump(p.Ctx, p.Native, &preserved); e != nil || preserved.String() != dump.String() {
				t.Fatal("reader failure changed state", e, preserved.String())
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1), "D", "restored"))
		run(fixture.Exec("INSERT INTO t VALUES(3,'d',FALSE,'ignored') ON CONFLICT(lower(v)) DO UPDATE SET p=excluded.p", 1))
		run(fixture.Select("SELECT k,v,p FROM t ORDER BY k", []string{"k", "v", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "D", "ignored"}, {int64(2), "C", "new"}}))
	})
	t.Run("physical_holder_teardown", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "UPDATE t SET v=20", 1)
			ap, cp := 0, 0
			if b.PG {
				ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
			}
			done := fixture.RowAsync(t, p.Ctx, c, "INSERT INTO t VALUES(2,2),(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", true, false)
			conflictUpdateWait(t, p, b, cp, ap)
			if e := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(e, driver.ErrBadConn) {
				t.Fatal("physical eviction", e)
			}
			r := fixture.LockResult(t, done)
			fixture.State(t, r.Err, "")
			fixture.LockAffected(t, r.Result, 2)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(11)}, []any{int64(2), int64(2)}))
		})
	})
	t.Run("retained_bytes", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(k INT UNIQUE,p BYTEA)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1,'\\x0102')", 1))
			retained, e := b.DB.QueryContext(p.Ctx, "SELECT p FROM t")
			if e != nil {
				t.Fatal(e)
			}
			defer retained.Close()
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1,'\\x0304'),(2,'\\x0506') ON CONFLICT(k) DO UPDATE SET p=excluded.p", 2))
			if !b.PG {
				var dump bytes.Buffer
				if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
					t.Fatal(e)
				}
				if e := stuntgres.Reset(p.Ctx, p.Native); e != nil {
					t.Fatal(e)
				}
				if e := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
					t.Fatal(e)
				}
			}
			if !retained.Next() {
				t.Fatal("retained bytes lost", retained.Err())
			}
			var value any
			if e := retained.Scan(&value); e != nil {
				t.Fatal(e)
			}
			data, ok := value.([]byte)
			if !ok || !bytes.Equal(data, []byte{1, 2}) {
				t.Fatalf("retained BYTEA %T %v", value, value)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained bytes tail", retained.Err())
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT k,p FROM t ORDER BY k", []string{"k", "p"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\x03\x04"}, {int64(2), "\x05\x06"}}))
		})
	})
}
