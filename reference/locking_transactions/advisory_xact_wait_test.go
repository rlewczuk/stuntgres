package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAdvisoryXactCancellationTimeout(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer, observer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_xact_wait_data(v integer)", 0)
		ap, bp := advisoryXactPID(t, p, b, a), advisoryXactPID(t, p, b, peer)
		earlier := advisorySessionKey{"98", 0, 98, 1}
		persistent := advisorySessionKey{"99", 0, 99, 1}
		for _, k := range advisorySessionKeys {
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_xact_lock"), "VOID", "")
			for _, prepared := range []bool{false, true} {
				q := k.call("pg_advisory_xact_lock")
				var x fixture.Executor = peer
				if prepared {
					x = advisoryXactHeld(t, p, peer, q)
				}
				for _, child := range []bool{false, true} {
					for _, budget := range []struct{ lock, statement, code, message string }{
						{"40ms", "2s", "55P03", "canceling statement due to lock timeout"},
						{"2s", "40ms", "57014", "canceling statement due to statement timeout"},
						{"40ms", "40ms", "57014", "canceling statement due to statement timeout"},
					} {
						advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
						advisorySessionValue(t, p.Ctx, peer, earlier.call("pg_advisory_xact_lock_shared"), "VOID", "")
						advisorySessionValue(t, p.Ctx, peer, persistent.call("pg_advisory_lock"), "VOID", "")
						if child {
							advisorySessionExec(t, p.Ctx, peer, "SAVEPOINT child", 0)
						}
						advisorySessionExec(t, p.Ctx, peer, "INSERT INTO advisory_xact_wait_data VALUES(1)", 1)
						advisorySessionValue(t, p.Ctx, peer, earlier.call("pg_advisory_xact_lock"), "VOID", "")
						advisorySessionExec(t, p.Ctx, peer, "SET LOCAL lock_timeout='"+budget.lock+"'", 0)
						advisorySessionExec(t, p.Ctx, peer, "SET LOCAL statement_timeout='"+budget.statement+"'", 0)
						advisorySessionError(t, p.Ctx, x, q, advisorySessionTimeout(budget.code, budget.message))
						advisorySessionError(t, p.Ctx, peer, "SELECT true", advisorySessionTimeout("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						advisoryXactModes(t, p, observer, k, bp)
						advisoryXactProbe(t, p, observer, earlier, true, true)
						advisoryXactProbe(t, p, observer, earlier, false, !child)
						advisoryXactProbe(t, p, observer, persistent, false, false)
						if child {
							advisorySessionExec(t, p.Ctx, peer, "ROLLBACK TO child", 0)
							advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
							fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM advisory_xact_wait_data")
						}
						advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
						fixture.PreparedReferenceRows(t, p, b, observer, "SELECT v FROM advisory_xact_wait_data")
						advisorySessionClear(t, p.Ctx, peer)
						advisoryXactProbe(t, p, peer, k, false, false)
					}
				}
				advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
				w := advisoryXactStart(t, p.Ctx, x, q)
				advisoryXactWait(t, p, observer, bp, ap, k, false)
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
					fixture.AssertDiagnostic(t, w.await(t), new(advisorySessionTimeout("57014", "canceling statement due to user request")))
				} else {
					w.cancel()
					if err := w.await(t); !errors.Is(err, context.Canceled) {
						t.Fatal("caller cancellation", err)
					}
				}
				advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
				advisoryXactModes(t, p, observer, k, bp)
				if !b.PG {
					ctx, cancel := context.WithTimeout(p.Ctx, 40*time.Millisecond)
					rows, err := x.QueryContext(ctx, q)
					if rows != nil {
						rows.Close()
					}
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("caller deadline", err)
					}
				}
			}
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
			advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, peer, k.call("pg_advisory_xact_lock"), "VOID", "")
			advisorySessionExec(t, p.Ctx, peer, "COMMIT", 0)
			// Canceled/timed-out attempts never acquire after the holder ends.
			advisoryXactProbe(t, p, observer, k, false, true)
		}
	})
}
