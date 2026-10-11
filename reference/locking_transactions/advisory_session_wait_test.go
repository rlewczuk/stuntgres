package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAdvisorySessionBlockingWake(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer, late := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, shared := range []bool{false, true} {
					name := "pg_advisory_lock"
					if shared {
						name += "_shared"
					}
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
					ap, bp := 0, 0
					if b.PG {
						ap = fixture.LockPID(t, p.Ctx, a)
						bp = fixture.LockPID(t, p.Ctx, peer)
					}
					w := advisorySessionStart(t, p.Ctx, peer, k.call(name))
					if b.PG {
						advisorySessionWait(t, p.Ctx, bp, ap, k, shared)
					}
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
					if err := w.await(t); err != nil {
						t.Fatal(err)
					}
					advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_lock"), "BOOL", false)
					advisorySessionClear(t, p.Ctx, peer)
				}
				if b.PG {
					// PG admission is observed. Native exact non-barging/reentry
					// registration is the private manager test, not worker timing.
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
					ap, bp, cp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer), fixture.LockPID(t, p.Ctx, late)
					x := advisorySessionStart(t, p.Ctx, peer, k.call("pg_advisory_lock"))
					advisorySessionWait(t, p.Ctx, bp, ap, k, false)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
					advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_lock_shared"), "BOOL", true)
					s := advisorySessionStart(t, p.Ctx, late, k.call("pg_advisory_lock_shared"))
					advisorySessionWait(t, p.Ctx, cp, bp, k, true)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionWait(t, p.Ctx, bp, ap, k, false)
					var canceled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
					fixture.AssertDiagnostic(t, x.await(t), new(advisorySessionTimeout("57014", "canceling statement due to user request")))
					if err := s.await(t); err != nil {
						t.Fatal(err)
					}
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_lock"), "BOOL", false)
					advisorySessionClear(t, p.Ctx, late)
				}
			}
		})
	}
}

func TestReferenceAdvisorySessionCancelTimeout(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
				for _, prepared := range []bool{false, true} {
					q := k.call("pg_advisory_lock")
					var exec fixture.Executor = peer
					if prepared {
						stmt, err := peer.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						exec = fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: q}
					}
					for _, budget := range []struct{ lock, statement, code, message string }{
						{"40ms", "2s", "55P03", "canceling statement due to lock timeout"},
						{"2s", "40ms", "57014", "canceling statement due to statement timeout"},
						{"40ms", "40ms", "57014", "canceling statement due to statement timeout"},
					} {
						// SET LOCAL rolls back to unarmed defaults before SAME owner
						// continuation, avoiding an armed-owner cleanup race.
						advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
						advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_lock(98)", "VOID", "")
						advisorySessionExec(t, p.Ctx, peer, "SET LOCAL lock_timeout='"+budget.lock+"'", 0)
						advisorySessionExec(t, p.Ctx, peer, "SET LOCAL statement_timeout='"+budget.statement+"'", 0)
						advisorySessionError(t, p.Ctx, exec, q, advisorySessionTimeout(budget.code, budget.message))
						advisorySessionError(t, p.Ctx, peer, "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
						advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
						advisorySessionValue(t, p.Ctx, a, "SELECT pg_try_advisory_lock(98)", "BOOL", false)
						advisorySessionValue(t, p.Ctx, peer, "SELECT pg_advisory_unlock(98)", "BOOL", true)
						advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
					}
				}
				ap, bp := 0, 0
				if b.PG {
					ap = fixture.LockPID(t, p.Ctx, a)
					bp = fixture.LockPID(t, p.Ctx, peer)
				}
				w := advisorySessionStart(t, p.Ctx, peer, k.call("pg_advisory_lock"))
				if b.PG {
					advisorySessionWait(t, p.Ctx, bp, ap, k, false)
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
					fixture.AssertDiagnostic(t, w.await(t), new(advisorySessionTimeout("57014", "canceling statement due to user request")))
				} else {
					w.cancel()
					if err := w.await(t); !errors.Is(err, context.Canceled) {
						t.Fatal("lost caller cancellation", err)
					}
					ctx, cancel := context.WithTimeout(p.Ctx, 40*time.Millisecond)
					var raw any
					err := peer.QueryRowContext(ctx, k.call("pg_advisory_lock")).Scan(&raw)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("lost caller deadline", err)
					}
				}
				advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
				advisorySessionClear(t, p.Ctx, a)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_advisory_lock"), "VOID", "")
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_advisory_unlock"), "BOOL", true)
				// No timed-out/canceled attempt contributed another count.
				advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_lock"), "BOOL", true)
				advisorySessionClear(t, p.Ctx, a)
			}
		})
	}
}
