package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceAdvisorySessionBlockingWake(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer, late := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, shared := range []bool{false, true} {
					name := "pg_advisory_lock"
					if shared {
						name += "_shared"
					}
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock"), "VOID", "")
					ap, bp := 0, 0
					if b.pg {
						ap = referenceLockPID(t, p.ctx, a)
						bp = referenceLockPID(t, p.ctx, peer)
					}
					w := advisorySessionStart(t, p.ctx, peer, k.call(name))
					if b.pg {
						advisorySessionWait(t, p.ctx, bp, ap, k, shared)
					}
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
					if err := w.await(t); err != nil {
						t.Fatal(err)
					}
					advisorySessionValue(t, p.ctx, a, k.call("pg_try_advisory_lock"), "BOOL", false)
					advisorySessionClear(t, p.ctx, peer)
				}
				if b.pg {
					// PG admission is observed. Native exact non-barging/reentry
					// registration is the private manager test, not worker timing.
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
					ap, bp, cp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, peer), referenceLockPID(t, p.ctx, late)
					x := advisorySessionStart(t, p.ctx, peer, k.call("pg_advisory_lock"))
					advisorySessionWait(t, p.ctx, bp, ap, k, false)
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
					advisorySessionValue(t, p.ctx, a, k.call("pg_try_advisory_lock_shared"), "BOOL", true)
					s := advisorySessionStart(t, p.ctx, late, k.call("pg_advisory_lock_shared"))
					advisorySessionWait(t, p.ctx, cp, bp, k, true)
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionWait(t, p.ctx, bp, ap, k, false)
					var canceled bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
					referenceAssertDiagnostic(t, x.await(t), new(advisorySessionTimeout("57014", "canceling statement due to user request")))
					if err := s.await(t); err != nil {
						t.Fatal(err)
					}
					advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
					advisorySessionValue(t, p.ctx, a, k.call("pg_try_advisory_lock"), "BOOL", false)
					advisorySessionClear(t, p.ctx, late)
				}
			}
		})
	}
}

func TestReferenceAdvisorySessionCancelTimeout(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock"), "VOID", "")
				for _, prepared := range []bool{false, true} {
					q := k.call("pg_advisory_lock")
					var exec referenceExecutor = peer
					if prepared {
						stmt, err := peer.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						exec = referenceHeldStatement{referenceExecutor: peer, statement: stmt, query: q}
					}
					for _, budget := range []struct{ lock, statement, code, message string }{
						{"40ms", "2s", "55P03", "canceling statement due to lock timeout"},
						{"2s", "40ms", "57014", "canceling statement due to statement timeout"},
						{"40ms", "40ms", "57014", "canceling statement due to statement timeout"},
					} {
						// SET LOCAL rolls back to unarmed defaults before SAME owner
						// continuation, avoiding an armed-owner cleanup race.
						advisorySessionExec(t, p.ctx, peer, "BEGIN", 0)
						advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_lock(98)", "VOID", "")
						advisorySessionExec(t, p.ctx, peer, "SET LOCAL lock_timeout='"+budget.lock+"'", 0)
						advisorySessionExec(t, p.ctx, peer, "SET LOCAL statement_timeout='"+budget.statement+"'", 0)
						advisorySessionError(t, p.ctx, exec, q, advisorySessionTimeout(budget.code, budget.message))
						advisorySessionError(t, p.ctx, peer, "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						advisorySessionExec(t, p.ctx, peer, "ROLLBACK", 0)
						advisorySessionValue(t, p.ctx, peer, "SELECT true", "BOOL", true)
						advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(98)", "BOOL", false)
						advisorySessionValue(t, p.ctx, peer, "SELECT pg_advisory_unlock(98)", "BOOL", true)
						advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
					}
				}
				ap, bp := 0, 0
				if b.pg {
					ap = referenceLockPID(t, p.ctx, a)
					bp = referenceLockPID(t, p.ctx, peer)
				}
				w := advisorySessionStart(t, p.ctx, peer, k.call("pg_advisory_lock"))
				if b.pg {
					advisorySessionWait(t, p.ctx, bp, ap, k, false)
					var ok bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
					referenceAssertDiagnostic(t, w.await(t), new(advisorySessionTimeout("57014", "canceling statement due to user request")))
				} else {
					w.cancel()
					if err := w.await(t); !errors.Is(err, context.Canceled) {
						t.Fatal("lost caller cancellation", err)
					}
					ctx, cancel := context.WithTimeout(p.ctx, 40*time.Millisecond)
					var raw any
					err := peer.QueryRowContext(ctx, k.call("pg_advisory_lock")).Scan(&raw)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("lost caller deadline", err)
					}
				}
				advisorySessionValue(t, p.ctx, peer, "SELECT true", "BOOL", true)
				advisorySessionClear(t, p.ctx, a)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_advisory_lock"), "VOID", "")
				advisorySessionValue(t, p.ctx, peer, k.call("pg_advisory_unlock"), "BOOL", true)
				// No timed-out/canceled attempt contributed another count.
				advisorySessionValue(t, p.ctx, a, k.call("pg_try_advisory_lock"), "BOOL", true)
				advisorySessionClear(t, p.ctx, a)
			}
		})
	}
}
