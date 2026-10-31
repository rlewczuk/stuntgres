package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionSignalCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					owner, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { owner.Close() })
					victim, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { victim.Close() })
					caller, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { caller.Close() })
					run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(owner, fixture.Exec("CREATE TABLE session_signal_cancel(v integer)", 0))
					run(owner, fixture.Exec("INSERT INTO session_signal_cancel VALUES(1)", 1))
					pid := fixture.LockPID(t, p.Ctx, victim)
					holder := fixture.LockPID(t, p.Ctx, owner)
					relation := int64(0)
					if b.PG {
						relation = fixture.LockOID(t, p.Ctx, "session_signal_cancel")
					}
					var stmt *sql.Stmt
					if prepared {
						stmt, err = victim.PrepareContext(p.Ctx, "INSERT INTO session_signal_cancel VALUES(9)")
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
					}
					// Prepare outside both explicit transactions, before the owner's
					// conflicting lock. Preparation has real relation admission too.
					run(owner, fixture.Exec("BEGIN", 0))
					t.Cleanup(func() { owner.ExecContext(context.Background(), "ROLLBACK") })
					run(owner, fixture.Exec("LOCK TABLE session_signal_cancel IN ACCESS EXCLUSIVE MODE", 0))
					run(victim, fixture.Exec("BEGIN", 0))
					t.Cleanup(func() { victim.ExecContext(context.Background(), "ROLLBACK") })
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						var e error
						if stmt != nil {
							_, e = stmt.ExecContext(ctx)
						} else {
							_, e = victim.ExecContext(ctx, "INSERT INTO session_signal_cancel VALUES(9)")
						}
						return fixture.RelationOutcome{Err: e}
					})
					fixture.RelationLockPending(t, p, b, pid, holder, relation, "RowExclusiveLock", finished)
					s := fixture.Select("SELECT pg_cancel_backend($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(caller, s)
					out := join()
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
					fixture.AssertDiagnostic(t, out.Err, &want)
					run(victim, signalError("SELECT 42", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					run(victim, fixture.Exec("ROLLBACK", 0))
					run(owner, fixture.Exec("COMMIT", 0))
					run(victim, fixture.Select("SELECT pg_backend_pid(),42", []string{"pg_backend_pid", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(pid), int64(42)}}))
					run(caller, fixture.Select("SELECT pg_cancel_backend($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid))
					run(victim, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
					run(caller, fixture.Select("SELECT v FROM session_signal_cancel", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}

func TestReferenceSessionSignalTermination(t *testing.T) {
	sessionSignalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, caller *sql.Conn, n *[]fixture.Diagnostic) {
		run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(caller, fixture.Exec("CREATE TABLE session_signal_terminate(v integer)", 0))
		run(caller, fixture.Exec("INSERT INTO session_signal_terminate VALUES(1)", 1))
		for _, timeout := range []string{"", "0", "1000"} {
			victim, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { victim.ExecContext(context.Background(), "ROLLBACK"); victim.Close() })
			pid := fixture.LockPID(t, p.Ctx, victim)
			run(victim, fixture.Exec("CREATE TEMP TABLE session_signal_temp(v integer)", 0))
			run(victim, fixture.Exec("PREPARE session_signal_prepared AS SELECT 42", 0))
			run(victim, fixture.Exec("LISTEN session_signal_channel", 0))
			run(victim, fixture.Select("SELECT pg_advisory_lock(73621)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
			run(victim, fixture.Exec("BEGIN", 0))
			run(victim, fixture.Exec("INSERT INTO session_signal_terminate VALUES(9)", 1))
			run(victim, fixture.Exec("LOCK TABLE session_signal_terminate IN ACCESS EXCLUSIVE MODE", 0))
			run(victim, fixture.Exec("DECLARE session_signal_cursor CURSOR FOR SELECT v FROM session_signal_terminate", 0))
			q := "SELECT pg_terminate_backend($1"
			if timeout != "" {
				q += "," + timeout
			}
			q += ") AS v"
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid)
			if timeout == "1000" {
				s = fixture.Prepared(s)
			}
			run(caller, s)
			// First use can expose the fatal or adapter retirement; neither may succeed.
			var answer int
			if e = victim.QueryRowContext(p.Ctx, "SELECT 42").Scan(&answer); e == nil {
				t.Fatal("terminated physical owner returned", answer)
			}
			if !b.PG {
				fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"})
			}
			victim.Close()
			run(caller, fixture.Exec("BEGIN", 0))
			run(caller, fixture.Exec("LOCK TABLE session_signal_terminate IN ACCESS EXCLUSIVE MODE", 0))
			run(caller, fixture.Exec("COMMIT", 0))
			run(caller, fixture.Select("SELECT pg_try_advisory_lock(73621)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{true}}))
			run(caller, fixture.Select("SELECT pg_advisory_unlock(73621)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
			run(caller, fixture.Select("SELECT v FROM session_signal_terminate", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(caller, fixture.Select("SELECT pg_cancel_backend($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, pid))
			signalNotices(t, n, sessionSignalWarning(pid))
			fresh, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { fresh.Close() })
			newPID := fixture.LockPID(t, p.Ctx, fresh)
			if newPID == pid {
				t.Fatal("retired identity reused", pid)
			}
			run(fresh, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fresh.Close()
		}
	})
}
