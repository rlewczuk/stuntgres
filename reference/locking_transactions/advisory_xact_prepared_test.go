package reference_test

import (
	"database/sql/driver"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisoryXactPreparedTransactions(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		peer := advisoryXactConn(t, p, b)
		advisorySessionExec(t, p.Ctx, peer, "CREATE TABLE advisory_xact_prepared_data(v integer)", 0)
		n := 0
		for _, k := range advisorySessionKeys {
			for _, shared := range []bool{false, true} {
				for _, end := range []string{"COMMIT", "ROLLBACK"} {
					a := advisoryXactConn(t, p, b)
					n++
					gid := fmt.Sprintf("advisory_xact_%d", n)
					different := advisorySessionKey{"700", 0, 700, 1}
					advisorySessionValue(t, p.Ctx, a, different.call("pg_advisory_lock"), "VOID", "")
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionExec(t, p.Ctx, a, fmt.Sprintf("INSERT INTO advisory_xact_prepared_data VALUES(%d)", n), 1)
					advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(true, shared, false)), "VOID", "")
					fixture.PreparedReferencePrepare(t, p, b, a, gid)
					fixture.PreparedReferenceOne(t, p, peer, gid)
					advisoryXactProbe(t, p, peer, different, false, false)
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_xact_lock"), "BOOL", false)
					advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
					// Closing the real origin releases only its persistent contribution.
					if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
						t.Fatal(err)
					}
					advisoryXactProbe(t, p, peer, different, false, true)
					advisoryXactProbe(t, p, peer, k, false, false)
					advisorySessionExec(t, p.Ctx, peer, end+" PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
					fixture.PreparedReferenceAbsent(t, p, peer, gid)
					advisoryXactProbe(t, p, peer, k, false, true)
					rows := [][]any{}
					if end == "COMMIT" {
						rows = [][]any{{int64(n)}}
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(fmt.Sprintf("SELECT v FROM advisory_xact_prepared_data WHERE v=%d", n), []string{"v"}, []string{"INT4"}, rows))
				}
			}
		}
		a := advisoryXactConn(t, p, b)
		for _, k := range advisorySessionKeys {
			for _, history := range []string{"same-mode", "different-mode", "session-child-rollback", "xact-child-rollback", "child-release", "different-space"} {
				t.Run(fmt.Sprint(k.space)+"/"+history, func(t *testing.T) {
					const gid = "advisory_xact_mixed"
					fixture.PreparedReferenceCleanup(t, b, gid)
					advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
					advisorySessionExec(t, p.Ctx, a, "INSERT INTO advisory_xact_prepared_data VALUES(900)", 1)
					if history == "session-child-rollback" {
						advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_xact_lock"), "VOID", "")
						advisorySessionExec(t, p.Ctx, a, "SAVEPOINT child", 0)
						advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
						advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO child", 0)
					} else {
						advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_lock"), "VOID", "")
						if history == "xact-child-rollback" || history == "child-release" {
							advisorySessionExec(t, p.Ctx, a, "SAVEPOINT child", 0)
						}
						name := "pg_advisory_xact_lock"
						if history == "different-mode" {
							name += "_shared"
						}
						target := k
						if history == "different-space" {
							target = advisorySessionKeys[2-k.space]
						}
						advisorySessionValue(t, p.Ctx, a, target.call(name), "VOID", "")
						if history == "xact-child-rollback" {
							advisorySessionExec(t, p.Ctx, a, "ROLLBACK TO child", 0)
						}
						if history == "child-release" {
							advisorySessionExec(t, p.Ctx, a, "RELEASE child", 0)
						}
					}
					if history == "xact-child-rollback" || history == "different-space" {
						advisorySessionExec(t, p.Ctx, a, "PREPARE TRANSACTION 'advisory_xact_mixed'", 0)
						fixture.PreparedReferenceOne(t, p, peer, gid)
						advisorySessionExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'advisory_xact_mixed'", 0)
					} else {
						fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'advisory_xact_mixed'", "0A000", "cannot PREPARE while holding both session-level and transaction-level locks on the same object")
					}
					fixture.PreparedReferenceAbsent(t, p, peer, gid)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM advisory_xact_prepared_data WHERE v=900")
					advisoryXactProbe(t, p, peer, k, false, false)
					advisorySessionClear(t, p.Ctx, a)
					advisoryXactProbe(t, p, peer, k, false, true)
				})
			}
		}
	})
}

func TestReferenceAdvisoryXactLockMetadata(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer, observer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		ap, bp := advisoryXactPID(t, p, b, a), advisoryXactPID(t, p, b, peer)
		keys := append([]advisorySessionKey{}, advisorySessionKeys...)
		keys = append(keys, advisorySessionKey{"-1::bigint", 4294967295, 4294967295, 1}, advisorySessionKey{"-1::integer,-2::integer", 4294967295, 4294967294, 2})
		for _, k := range keys {
			for _, shared := range []bool{false, true} {
				advisorySessionValue(t, p.Ctx, a, k.call(advisoryXactName(false, shared, false)), "VOID", "")
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				for range 3 {
					advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_xact_lock"), "VOID", "")
				}
				modes := []string{"ExclusiveLock"}
				if shared {
					modes = append(modes, "ShareLock")
				}
				advisoryXactModes(t, p, observer, k, ap, modes...)
				advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_xact_lock"), "BOOL", false)
				advisoryXactModes(t, p, observer, k, bp)
				w := advisoryXactStart(t, p.Ctx, peer, k.call("pg_advisory_xact_lock"))
				advisoryXactWait(t, p, observer, bp, ap, k, false)
				advisorySessionExec(t, p.Ctx, a, "COMMIT", 0)
				mode := "ExclusiveLock"
				if shared {
					mode = "ShareLock"
				}
				advisoryXactModes(t, p, observer, k, ap, mode)
				advisorySessionClear(t, p.Ctx, a)
				if err := w.await(t); err != nil {
					t.Fatal(err)
				}
				advisoryXactModes(t, p, observer, k, bp, "ExclusiveLock")
				advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
				if rows := advisoryXactRows(t, p.Ctx, observer, k); len(rows) != 0 {
					t.Fatal(rows)
				}
			}
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_xact_lock"), "VOID", "")
			fixture.PreparedReferencePrepare(t, p, b, a, "advisory_xact_metadata")
			rows := advisoryXactRows(t, p.Ctx, observer, k)
			if len(rows) != 1 || rows[0].PID.Valid || rows[0].Mode != "ExclusiveLock" || !rows[0].Granted || rows[0].Waiting {
				t.Fatal("prepared advisory row", rows)
			}
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, k.call("pg_try_advisory_xact_lock"), "BOOL", false)
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
			advisorySessionExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'advisory_xact_metadata'", 0)
			if rows := advisoryXactRows(t, p.Ctx, observer, k); len(rows) != 0 {
				t.Fatal(rows)
			}
		}
	})
}
