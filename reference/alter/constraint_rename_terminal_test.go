package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameCancellation(t *testing.T) {
	for _, setting := range []string{"statement_timeout", "lock_timeout"} {
		for _, prepared := range []bool{false, true} {
			t.Run(setting+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT old UNIQUE)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					before := constraintRenameCapture(t, p, c, "old", true)
					q := "ALTER TABLE t RENAME CONSTRAINT old TO new"
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					x := fixture.Executor(c)
					if stmt != nil {
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS SHARE MODE", 0)
					run(fixture.Exec("SET "+setting+"='500ms'", 0))
					done := make(chan fixture.RelationOutcome, 1)
					go func() {
						result, err := x.ExecContext(p.Ctx, q)
						done <- fixture.RelationOutcome{Result: result, Err: err}
					}()
					out := fixture.LockResult(t, done)
					code, message := "57014", "canceling statement due to statement timeout"
					if setting == "lock_timeout" {
						code, message = "55P03", "canceling statement due to lock timeout"
					}
					want := constraintRenameError(code, message)
					fixture.AssertDiagnostic(t, out.Err, &want)
					if out.Result != nil {
						t.Fatal("canceled rename returned result")
					}
					fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
					run(fixture.Exec("RESET "+setting, 0))
					if constraintRenameCapture(t, p, c, "old", true) != before {
						t.Fatal("cancellation published group")
					}
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						if result, err := x.ExecContext(ctx, q); err == nil || result != nil {
							t.Fatal("precanceled rename succeeded", result, err)
						}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
					if constraintRenameCapture(t, p, c, "new", true) != before {
						t.Fatal("retry changed identity")
					}
				})
			})
		}
	}
}
