package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisoryXactCompatibilityBlocking(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer, observer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		ap, bp := advisoryXactPID(t, p, b, a), advisoryXactPID(t, p, b, peer)
		for _, k := range advisorySessionKeys {
			for _, holderXact := range []bool{false, true} {
				for _, requestXact := range []bool{false, true} {
					for _, holderShared := range []bool{false, true} {
						for _, requestShared := range []bool{false, true} {
							t.Run(fmt.Sprint(k.space, holderXact, requestXact, holderShared, requestShared), func(t *testing.T) {
								advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
								advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
								advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(holderXact, holderShared, false)), "VOID", "")
								compatible := holderShared && requestShared
								advisorySessionValue(t, p.Ctx, peer, k.call(advisoryXactName(requestXact, requestShared, true)), "BOOL", compatible)
								advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
								if compatible {
									advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
									advisorySessionClear(t, p.Ctx, peer)
								} else {
									// An unavailable try has no pending row or grant.
									advisoryXactModes(t, p, observer, k, bp)
									for _, prepared := range []bool{false, true} {
										if prepared {
											advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
											advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
										}
										q := k.call(advisoryXactName(requestXact, requestShared, false))
										var x fixture.Executor = peer
										if prepared {
											x = advisoryXactHeld(t, p, peer, q)
										}
										w := advisoryXactStart(t, p.Ctx, x, q)
										advisoryXactWait(t, p, observer, bp, ap, k, requestShared)
										if holderXact {
											end := "COMMIT"
											if prepared {
												end = "ROLLBACK"
											}
											advisorySessionExec(t, p.Ctx, a, end, 0)
										} else {
											unlock := "pg_advisory_unlock"
											if holderShared {
												unlock += "_shared"
											}
											advisorySessionValue(t, p.Ctx, a, k.call(unlock), "BOOL", true)
										}
										if err := w.await(t); err != nil {
											t.Fatal(err)
										}
										advisoryXactProbe(t, p, observer, k, false, false)
										advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
										advisorySessionClear(t, p.Ctx, peer)
										if !prepared {
											if holderXact {
												advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
											}
											advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(holderXact, holderShared, false)), "VOID", "")
										}
									}
								}
								advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
								advisorySessionClear(t, p.Ctx, a)
								advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
								advisorySessionClear(t, p.Ctx, peer)
							})
						}
					}
				}
			}
			// Existing hold on the opposite lifetime permits same-participant priority
			// over a writer that is itself blocked by this participant.
			for _, holderXact := range []bool{false, true} {
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(holderXact, true, false)), "VOID", "")
				w := advisoryXactStart(t, p.Ctx, peer, k.call("pg_advisory_xact_lock"))
				advisoryXactWait(t, p, observer, bp, ap, k, false)
				advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(!holderXact, true, true)), "BOOL", true)
				advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(!holderXact, false, true)), "BOOL", true)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				advisorySessionClear(t, p.Ctx, a)
				if err := w.await(t); err != nil {
					t.Fatal(err)
				}
				advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
			}
			// An external shared grant still prevents a cross-lifetime upgrade.
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
			advisorySessionValue(t, p.Ctx, peer, k.call("pg_advisory_lock_shared"), "VOID", "")
			advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_xact_lock"), "BOOL", false)
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
			advisorySessionClear(t, p.Ctx, a)
			advisorySessionClear(t, p.Ctx, peer)
		}
	})
}
