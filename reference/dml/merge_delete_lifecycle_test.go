package reference_test

import (
	"database/sql/driver"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeDeletePrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
			run(fixture.Exec("CREATE TABLE source(id INT)", 0))
			run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND $1::boolean THEN DELETE"
			held, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, v := range []any{false, nil} {
				r, e := held.ExecContext(p.Ctx, v)
				mergeInsertAffected(t, b, r, e, 0)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			}
			r, err := held.ExecContext(p.Ctx, "bad")
			if r != nil {
				t.Fatal("bad bind returned result")
			}
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
			d.Context = "unnamed portal parameter $1 = '...'"
			fixture.AssertDiagnostic(t, err, &d)
			r, err = held.ExecContext(p.Ctx, true)
			mergeInsertAffected(t, b, r, err, 1)
			run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
			run(fixture.Exec("INSERT INTO t VALUES(1,11)", 1))
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN id TO renamed", 0))
			r, err = held.ExecContext(p.Ctx, true)
			if r != nil {
				t.Fatal("renamed source returned result")
			}
			missing := fixture.ControlsReferenceDiagnostic("42703", "column s.id does not exist")
			missing.Hint = `Perhaps you meant to reference the column "t.id".`
			missing.Position = fixture.Position(strings.Index(q, "s.id") + 1)
			fixture.AssertDiagnostic(t, err, &missing)
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN renamed TO id", 0))
			r, err = held.ExecContext(p.Ctx, true)
			mergeInsertAffected(t, b, r, err, 1)
		})
	}
}

func TestReferenceMergeDeleteCommandMetadata(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,big BIGINT,note TEXT,flag BOOL,payload BYTEA,optional INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,2147483648,'old',TRUE,'\\x0102',NULL),(2,9223372036854775807,'survivor',FALSE,'\\x00ff',NULL)", 2))
		q := "MERGE INTO t USING (VALUES($1::integer)) s(id) ON t.id=s.id WHEN MATCHED AND $2::boolean THEN DELETE"
		s := fixture.Select(q, []string{}, []string{}, [][]any{}, 1, true)
		s.Prepared = prepared
		run(s)
		run(fixture.Select("SELECT * FROM t", []string{"id", "big", "note", "flag", "payload", "optional"}, []string{"INT4", "INT8", "TEXT", "BOOL", "BYTEA", "INT4"}, [][]any{{int64(2), int64(9223372036854775807), "survivor", false, string([]byte{0, 255}), nil}}))
		var first, second []byte
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT payload,payload FROM t").Scan(&first, &second); err != nil || len(first) != 2 || first[0] != 0 || first[1] != 255 || len(second) != 2 {
			t.Fatal("raw BYTEA survivor", first, second, err)
		}
		first[0] = 99
		if second[0] != 0 || second[1] != 255 {
			t.Fatal("duplicate output shared mutable bytes", second)
		}
		run(fixture.Select("SELECT payload FROM t", []string{"payload"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 255})}}))
		if !b.PG {
			c, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			err = c.Raw(func(raw any) error {
				stmt, err := raw.(driver.Conn).Prepare(q)
				if err != nil {
					return err
				}
				defer stmt.Close()
				if stmt.NumInput() != 2 {
					t.Fatalf("NumInput=%d want2", stmt.NumInput())
				}
				r, err := stmt.Exec([]driver.Value{int64(2), false})
				fixture.InsertSelectAffected(t, r, err, 0)
				if _, err = r.LastInsertId(); err == nil {
					t.Fatal("MERGE supplied LastInsertId")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceMergeDeleteTransactions(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
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
		survivor := [][]any{{int64(2), int64(20)}}
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE"
		command("BEGIN", 0)
		command(q, 1)
		read(owner, survivor)
		read(peer, original)
		command("ROLLBACK", 0)
		read(peer, original)
		command("BEGIN", 0)
		command(q, 1)
		command("COMMIT", 0)
		read(peer, survivor)
		command("BEGIN", 0)
		command("UPDATE t SET v=21 WHERE id=2", 1)
		command("SAVEPOINT keep", 0)
		s := fixture.ImplicitError("MERGE INTO t USING (VALUES(2),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE", mergeUpdateCardinalityDiagnostic())
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, s)
		aborted := fixture.ImplicitError("UPDATE t SET v=0", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.ErrorStage = "execute"
		fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, aborted)
		command("ROLLBACK TO keep", 0)
		read(owner, [][]any{{int64(2), int64(21)}})
		read(peer, survivor)
		command("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN DELETE", 1)
		read(owner, nil)
		read(peer, survivor)
		command("ROLLBACK", 0)
		read(peer, survivor)
	})
}
