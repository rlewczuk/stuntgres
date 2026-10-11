package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				own := func(s fixture.Step) {
					if strings.HasPrefix(s.Query, "MERGE") {
						s.Prepared = prepared
						if b.PG {
							s.Affected = 0
						}
					}
					fixture.InsertSelectRun(t, p.Ctx, tx, b.Recorder, s)
				}
				own(fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 1))
				read := func(values [][]any) fixture.Step {
					return fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, values)
				}
				own(read([][]any{{int64(1)}, {int64(99)}}))
				run(read([][]any{{int64(99)}}))
				own(fixture.Exec("SAVEPOINT keep", 0))
				d := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(2) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"}
				s := fixture.ImplicitError("MERGE INTO t USING (VALUES(2),(2)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", d)
				s.ErrorStage = "execute"
				own(s)
				own(fixture.ImplicitError("MERGE INTO t USING (VALUES(3)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				own(fixture.Exec("ROLLBACK TO SAVEPOINT keep", 0))
				own(read([][]any{{int64(1)}, {int64(99)}}))
				own(fixture.Exec("MERGE INTO t USING (VALUES(3)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 1))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{int64(99)}}
				if commit {
					want = [][]any{{int64(1)}, {int64(3)}, {int64(99)}}
				}
				run(read(want))
			})
		})
	}
}

func TestReferenceMergeInsertPreparedRebind(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			run(fixture.Exec("CREATE TABLE t(id INT,v TEXT DEFAULT 'old')", 0))
			run(fixture.Exec("CREATE TABLE source(id INT)", 0))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			exec := func(want int64) { result, err := stmt.ExecContext(p.Ctx); mergeInsertAffected(t, b, result, err, want) }
			exec(0)
			run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
			exec(1)
			run(fixture.Exec("INSERT INTO source VALUES(2)", 1))
			run(fixture.Exec("INSERT INTO t VALUES(2,'preexisting')", 1))
			exec(0)
			run(fixture.Exec("ALTER TABLE t ALTER COLUMN v SET DEFAULT 'new'", 0))
			run(fixture.Exec("INSERT INTO source VALUES(3)", 1))
			exec(1)
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "preexisting"}, {int64(3), "new"}}))
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN id TO renamed", 0))
			result, err := stmt.ExecContext(p.Ctx)
			if result != nil {
				t.Fatal("failed held MERGE exposed result")
			}
			d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column s.id does not exist", Hint: `Perhaps you meant to reference the column "t.id".`, Position: fixture.Position(strings.Index(q, "s.id") + 1)}
			fixture.AssertDiagnostic(t, err, &d)
			run(fixture.Exec("ALTER TABLE source RENAME COLUMN renamed TO id", 0))
			exec(0)
		})
	}
}

func TestReferenceMergeInsertCommandMetadata(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
		q := "MERGE INTO t USING (VALUES($1::integer)) s(id) ON t.id=s.id WHEN NOT MATCHED AND s.id=$1 THEN INSERT VALUES(s.id,'typed')"
		run(fixture.Exec(q, 1, int64(1)))
		command := fixture.Select(q, []string{}, []string{}, [][]any{}, int64(1))
		command.Prepared = prepared
		run(command)
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "typed"}}))
		if !b.PG {
			conn, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				stmt, err := raw.(driver.Conn).Prepare(q)
				if err != nil {
					return err
				}
				defer stmt.Close()
				if stmt.NumInput() != 1 {
					t.Fatalf("MERGE NumInput=%d", stmt.NumInput())
				}
				result, err := stmt.Exec([]driver.Value{int64(2)})
				fixture.InsertSelectAffected(t, result, err, 1)
				result, err = stmt.Exec([]driver.Value{int64(2)})
				fixture.InsertSelectAffected(t, result, err, 0)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "typed"}, {int64(2), "typed"}}))
		}
	})
}

func TestReferenceMergeInsertReadOnly(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		for _, q := range []string{
			"MERGE INTO t USING (SELECT 2 AS id WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
			"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
			"MERGE INTO t USING (VALUES(2)) s(id) ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT VALUES(s.id)",
		} {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute MERGE in a read-only transaction"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, s)
			fixture.InsertSelectRun(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			c.Close()
		}
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceMergeInsertCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := fixture.MergeRun(t, p, b, false)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("CREATE TABLE s(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
			run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
			q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
			if !b.PG {
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := b.DB.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("direct cancellation result=%v err=%v", result, err)
				}
				result, err = stmt.ExecContext(ctx)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("held cancellation result=%v err=%v", result, err)
				}
			} else {
				for _, prepared := range []bool{false, true} {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					var exec func(context.Context) fixture.RelationOutcome
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						exec = func(ctx context.Context) fixture.RelationOutcome {
							r, e := stmt.ExecContext(ctx)
							return fixture.RelationOutcome{Result: r, Err: e}
						}
					} else {
						exec = func(ctx context.Context) fixture.RelationOutcome {
							r, e := c.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: e}
						}
					}
					holder, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.InsertSelectRun(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE s IN ACCESS EXCLUSIVE MODE", 0))
					pid := fixture.LockPID(t, p.Ctx, c)
					join := fixture.LockOperation(t, p.Ctx, exec)
					fixture.LockWait(t, p.Ctx, pid)
					var canceled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
						t.Fatalf("server cancel=%v %v", canceled, err)
					}
					o := join()
					if o.Result != nil {
						t.Fatal("canceled MERGE exposed result")
					}
					d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
					fixture.AssertDiagnostic(t, o.Err, &d)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			run(fixture.Exec(q, 1))
		})
	}
}
