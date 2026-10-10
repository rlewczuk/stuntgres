package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceTimeoutChainPrepared(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "aborted-COMMIT", "aborted-ROLLBACK"} {
		t.Run(end, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				f := newTimeoutReferenceFixture(t, p, b)
				f.set("transaction_timeout", "1s")
				f.beginPrivate()
				f.exec(f.owner, "SET LOCAL transaction_timeout='500ms'", 0)
				command := end
				aborted := end == "aborted-COMMIT" || end == "aborted-ROLLBACK"
				if aborted {
					_, err := f.owner.ExecContext(p.ctx, "SELECT timeout_missing")
					want := controlsReferenceDiagnostic("42703", `column "timeout_missing" does not exist`)
					want.Position = "8"
					referenceAssertDiagnostic(t, err, &want)
					command = "COMMIT"
					if end == "aborted-ROLLBACK" {
						command = "ROLLBACK"
					}
				}
				timeoutReferenceIdle(t, p.ctx, 600*time.Millisecond)
				f.exec(f.owner, command+" AND CHAIN", 0)
				f.show("transaction_timeout", "1s")
				// Cross old BEGIN's deadline and old LOCAL duration without
				// crossing the new incarnation's full restored one-second budget.
				timeoutReferenceIdle(t, p.ctx, 600*time.Millisecond)
				f.show("transaction_timeout", "1s")
				timeoutReferenceIdle(t, p.ctx, 600*time.Millisecond)
				f.fatalProbe("25P04")
				if end == "COMMIT" {
					f.rows(f.peer, 7, 8)
				} else {
					f.rows(f.peer, 7)
				}
			})
		})
	}
	t.Run("detached-survives-origin-idle", func(t *testing.T) {
		timeoutReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			f := newTimeoutReferenceFixture(t, p, b)
			const gid = "timeout_detached"
			preparedReferenceCleanup(t, b, gid)
			f.set("transaction_timeout", "500ms")
			f.set("idle_session_timeout", "750ms")
			f.beginPrivate()
			f.exec(f.owner, "PREPARE TRANSACTION '"+gid+"'", 0)
			entry := preparedReferenceOne(t, p, f.peer, gid)
			timeoutReferenceIdle(t, p.ctx, 1000*time.Millisecond)
			f.fatalProbe("57P05")
			got := preparedReferenceOne(t, p, f.peer, gid)
			expectedZone, expectedOffset := entry.prepared.Zone()
			gotZone, gotOffset := got.prepared.Zone()
			if got.xid != entry.xid || got.gid != entry.gid || got.owner != entry.owner || got.database != entry.database ||
				!got.prepared.Equal(entry.prepared) || gotZone != expectedZone || gotOffset != expectedOffset {
				t.Fatal("origin timeout changed detached entry", entry, got)
			}
			f.exec(f.peer, "BEGIN", 0)
			_, err := f.peer.ExecContext(p.ctx, "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE NOWAIT")
			referenceWaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "timeout_sentinel"`)
			f.exec(f.peer, "ROLLBACK", 0)
			f.exec(f.peer, "COMMIT PREPARED '"+gid+"'", 0)
			preparedReferenceAbsent(t, p, f.peer, gid)
			f.rows(f.peer, 7, 8)
		})
	})
}

func TestReferenceTimeoutFatalCleanup(t *testing.T) {
	for _, code := range []string{"25P04", "25P03"} {
		t.Run(code, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				f := newTimeoutReferenceFixture(t, p, b)
				f.exec(f.owner, "CREATE TEMP TABLE timeout_owner_temp(v INT)", 0)
				f.exec(f.owner, "INSERT INTO timeout_owner_temp VALUES(11)", 1)
				f.exec(f.peer, "CREATE TEMP TABLE timeout_peer_temp(v INT)", 0)
				f.exec(f.peer, "INSERT INTO timeout_peer_temp VALUES(22)", 1)
				referenceRunStep(t, p.ctx, f.peer, b.recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='timeout_owner_temp'", []string{"relname"}, []string{"NAME"}, [][]any{{"timeout_owner_temp"}}))
				held, err := f.owner.PrepareContext(p.ctx, "SELECT v FROM timeout_owner_temp")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { held.Close() })
				op, pp := 0, 0
				if b.pg {
					op = referenceLockPID(t, p.ctx, f.owner)
					pp = referenceLockPID(t, p.ctx, f.peer)
				}
				setting := "transaction_timeout"
				if code == "25P03" {
					setting = "idle_in_transaction_session_timeout"
				}
				f.set(setting, "750ms")
				f.beginPrivate()
				await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					r, e := f.peer.QueryContext(ctx, "SELECT v FROM timeout_sentinel ORDER BY v")
					return relationOutcome{rows: r, err: e}
				})
				if b.pg {
					referenceLockWaitBehind(t, p.ctx, pp, op, referenceLockOID(t, p.ctx, "public.timeout_sentinel"), "AccessShareLock")
				}
				timeoutReferenceIdle(t, p.ctx, 1000*time.Millisecond)
				f.fatalProbe(code)
				o := await()
				referenceLimitLockValues(t, o, b.pg, 7)
				// The catalog witness tests actual TEMP disposal, not merely loss
				// of lookup through an already unusable physical owner.
				f.tempAbsent()
				referenceRunStep(t, p.ctx, f.peer, b.recorder, referenceSelect("SELECT v FROM timeout_peer_temp", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
				rows, err := held.QueryContext(p.ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("terminated handle published rows")
				}
				timeoutReferenceDeadError(t, err)
				referenceRunStep(t, p.ctx, f.peer, b.recorder, localReferenceSelect(b.pg, "SHOW "+setting, setting, "0"))
				f.rows(f.peer, 7)
			})
		})
	}
}
