//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreRowSQLDeadlines(t *testing.T) {
	for _, setting := range []struct{ name, code, message string }{{"lock_timeout", "55P03", "canceling statement due to lock timeout"}, {"statement_timeout", "57014", "canceling statement due to statement timeout"}} {
		for _, prepared := range []bool{false, true} {
			t.Run(setting.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewPair(t)
				for i, db := range []*sql.DB{p.Memory, p.Postgres} {
					t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						a, b := fixture.LockConnections(t, ctx, db)
						fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
						fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
						var pid int
						if i == 1 {
							if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
								t.Fatal(err)
							}
						}
						q := "UPDATE t SET v=3"
						var stmt *sql.Stmt
						var err error
						if prepared {
							stmt, err = b.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.LockExec(t, ctx, tx, "UPDATE t SET v=2", 1)
						fixture.LockExec(t, ctx, b, "BEGIN", 0)
						defer func() {
							cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
							defer stop()
							for _, q := range []string{"ROLLBACK", "SET lock_timeout=0", "SET statement_timeout=0"} {
								if _, err := b.ExecContext(cleanup, q); err != nil {
									t.Error("deadline fixture cleanup", q, err)
								}
							}
						}()
						fixture.LockExec(t, ctx, b, "SET "+setting.name+"='750ms'", 0)
						done := make(chan fixture.RelationOutcome, 1)
						joined := false
						go func() {
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = b.ExecContext(ctx, q)
							}
							done <- fixture.RelationOutcome{Result: result, Err: err}
						}()
						defer func() {
							cancel()
							if !joined {
								select {
								case <-done:
								case <-time.After(5 * time.Second):
									t.Error("row deadline worker not joined")
								}
							}
						}()
						if i == 1 {
							referenceUpdateWait(t, ctx, pid)
						} // pg_blocking_pids plus ungranted resource, not a sleep claim.
						outcome := fixture.LockResult(t, done)
						joined = true
						if outcome.Result != nil {
							t.Fatal("timeout exposed a successful result")
						}
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: setting.code, Message: setting.message}
						if i == 1 {
							got := fixture.ExtractDiagnostic(t, outcome.Err)
							if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`).MatchString(got.Context) {
								t.Fatal("row SQL deadline tuple context", got)
							}
							want.Context = got.Context // Structural identity, not an invented physical tuple number.
						}
						fixture.AssertDiagnostic(t, outcome.Err, &want)
						_, err = b.ExecContext(ctx, "UPDATE t SET v=4")
						aborted := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
						fixture.AssertDiagnostic(t, err, &aborted)
						fixture.LockExec(t, ctx, b, "ROLLBACK", 0)
						fixture.LockExec(t, ctx, b, "SET "+setting.name+"=0", 0)
						if err = tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						var old int64
						if err = b.QueryRowContext(ctx, "SELECT v FROM t").Scan(&old); err != nil || old != 1 {
							t.Fatal("timeout partial effects", old, err)
						}
						fixture.LockExec(t, ctx, b, "UPDATE t SET v=5", 1)
						var current int64
						if err = b.QueryRowContext(ctx, "SELECT v FROM t").Scan(&current); err != nil || current != 5 {
							t.Fatal("row not reacquired", current, err)
						}
					})
				}
			})
		}
	}
}
