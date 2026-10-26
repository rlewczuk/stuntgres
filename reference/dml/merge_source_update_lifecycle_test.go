package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeSourceUpdatePrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			run(fixture.Exec("CREATE TABLE t(id INT,v INT,note TEXT)", 0))
			run(fixture.Exec("CREATE TABLE source(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10,'matched'),(2,20,'old')", 2))
			run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=$1::integer,note=$2::text"
			held, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, tc := range []struct {
				value, note any
				want        [][]any
			}{
				{30, "good", [][]any{{int64(1), int64(10), "matched"}, {int64(2), int64(30), "good"}}},
				{nil, nil, [][]any{{int64(1), int64(10), "matched"}, {int64(2), nil, nil}}},
			} {
				r, e := held.ExecContext(p.Ctx, tc.value, tc.note)
				mergeInsertAffected(t, b, r, e, 1)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, tc.want))
			}
			r, err := held.ExecContext(p.Ctx, "bad", "bad")
			if r != nil {
				t.Fatal("bad parameter exposed result")
			}
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			d.Context = "unnamed portal parameter $1 = '...'"
			fixture.AssertDiagnostic(t, err, &d)
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(10), "matched"}, {int64(2), nil, nil}}))
			r, err = held.ExecContext(p.Ctx, 40, "recovered")
			mergeInsertAffected(t, b, r, err, 1)
			run(fixture.Exec("UPDATE source SET id=2", 1))
			r, err = held.ExecContext(p.Ctx, 50, "changed source")
			mergeInsertAffected(t, b, r, err, 1)
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(50), "changed source"}, {int64(2), int64(40), "recovered"}}))
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN id TO renamed", 0))
			r, err = held.ExecContext(p.Ctx, 60, "bad schema")
			if r != nil {
				t.Fatal("invalid rebound schema exposed result")
			}
			missing := fixture.ControlsReferenceDiagnostic("42703", "column s.id does not exist")
			missing.Hint = `Perhaps you meant to reference the column "t.id".`
			missing.Position = fixture.Position(strings.Index(q, "s.id") + 1)
			fixture.AssertDiagnostic(t, err, &missing)
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN renamed TO id", 0))
			r, err = held.ExecContext(p.Ctx, 60, "repaired")
			mergeInsertAffected(t, b, r, err, 1)
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(60), "repaired"}, {int64(2), int64(40), "recovered"}}))
		})
	}
}

func TestReferenceMergeSourceUpdateCommandMetadata(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		q := "MERGE INTO t USING (VALUES($1::integer)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+$2::integer"
		run(fixture.Exec(q, 1, 1, 2))
		s := fixture.Select(q, []string{}, []string{}, [][]any{}, 1, 3)
		s.Prepared = prepared
		run(s)
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(25)}}))
		if !b.PG {
			c, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = c.Raw(func(raw any) error {
				stmt, e := raw.(driver.Conn).Prepare(q)
				if e != nil {
					return e
				}
				defer stmt.Close()
				if stmt.NumInput() != 2 {
					t.Fatalf("NumInput=%d want2", stmt.NumInput())
				}
				r, e := stmt.Exec([]driver.Value{int64(1), int64(1)})
				fixture.InsertSelectAffected(t, r, e, 1)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(26)}}))
		}
	})
}

func TestReferenceMergeSourceUpdateTransactions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, false)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		command := func(q string, n int64) {
			s := fixture.Exec(q, n)
			if strings.HasPrefix(q, "MERGE") {
				s.Prepared = prepared
				if b.PG {
					s.Affected = 0
				}
			}
			fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, s)
		}
		read := func(x fixture.Executor, rows [][]any) {
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
		}
		original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=30"
		command("BEGIN", 0)
		command(q, 1)
		read(owner, [][]any{{int64(1), int64(10)}, {int64(2), int64(30)}})
		read(peer, original)
		command("ROLLBACK", 0)
		read(peer, original)
		command("BEGIN", 0)
		command(q, 1)
		command("COMMIT", 0)
		read(peer, [][]any{{int64(1), int64(10)}, {int64(2), int64(30)}})
		command("BEGIN", 0)
		command("UPDATE t SET v=40 WHERE id=1", 1)
		command("SAVEPOINT keep", 0)
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(1),(3)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=50 WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,33) WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=100/(t.id-2)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, s)
		aborted := fixture.ImplicitError("UPDATE t SET v=0", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, aborted)
		command("ROLLBACK TO keep", 0)
		command("RELEASE keep", 0)
		read(owner, [][]any{{int64(1), int64(40)}, {int64(2), int64(30)}})
		command(q, 1)
		command("COMMIT", 0)
		read(peer, [][]any{{int64(1), int64(40)}, {int64(2), int64(30)}})
	})
}

func TestReferenceMergeSourceUpdateCancellation(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		q := "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=99"
		// database/sql rejects a pre-canceled call before backend dispatch, both modes.
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		var result interface{ RowsAffected() (int64, error) }
		var err error
		if prepared {
			held, e := b.DB.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			result, err = held.ExecContext(ctx)
			if e = held.Close(); e != nil {
				t.Fatal(e)
			}
		} else {
			result, err = b.DB.ExecContext(ctx, q)
		}
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled command", result, err)
		}
		holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=21 WHERE id=2", 1)
		fixture.LockExec(t, p.Ctx, worker, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, worker, "SET LOCAL lock_timeout='10ms'", 0)
		out := fixture.RowExecute(p.Ctx, worker, q, prepared)
		if out.Result != nil {
			t.Fatal("timeout exposed result")
		}
		d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
		got := fixture.ExtractDiagnostic(t, out.Err)
		if b.PG {
			d.Context = got.Context
			if !strings.HasPrefix(d.Context, "while updating tuple (") || !strings.HasSuffix(d.Context, `) in relation "t"`) {
				t.Fatal("tuple timeout context", got)
			}
		}
		fixture.AssertDiagnostic(t, out.Err, &d)
		fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		run(fixture.Exec(q, 2))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(99)}}))
	})
}
