package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeoutChainPrepared(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "aborted-COMMIT", "aborted-ROLLBACK"} {
		t.Run(end, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				const transactionTimeout = 6 * time.Second
				const localTimeout = time.Second
				f.set("transaction_timeout", "6s")
				beginBefore := time.Now()
				f.exec(f.owner, "BEGIN", 0)
				beginAfter := time.Now()
				f.exec(f.owner, "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE", 0)
				f.exec(f.owner, "INSERT INTO timeout_sentinel VALUES(8)", 1)
				f.exec(f.owner, "SET LOCAL transaction_timeout='1s'", 0)
				command := end
				aborted := end == "aborted-COMMIT" || end == "aborted-ROLLBACK"
				if aborted {
					_, err := f.owner.ExecContext(p.Ctx, "SELECT timeout_missing")
					want := fixture.ControlsReferenceDiagnostic("42703", `column "timeout_missing" does not exist`)
					want.Position = "8"
					fixture.AssertDiagnostic(t, err, &want)
					command = "COMMIT"
					if end == "aborted-ROLLBACK" {
						command = "ROLLBACK"
					}
				}
				timeoutReferenceIdle(t, p.Ctx, max(0, time.Until(beginAfter.Add(3*time.Second))))
				chainBefore := time.Now()
				if !chainBefore.Before(beginBefore.Add(transactionTimeout)) {
					t.Fatalf("inconclusive CHAIN window overrun: BEGIN bracket=[%s, %s], CHAIN start=%s, original budget=%s",
						beginBefore, beginAfter, chainBefore, transactionTimeout)
				}
				f.exec(f.owner, command+" AND CHAIN", 0)
				chainAfter := time.Now()
				f.show("transaction_timeout", "6s")

				// Cross the latest possible old BEGIN deadline and the old LOCAL
				// duration, but stay before the earliest possible new deadline.
				survivalAt := beginAfter.Add(transactionTimeout)
				if localDeadline := chainAfter.Add(localTimeout); localDeadline.After(survivalAt) {
					survivalAt = localDeadline
				}
				survivalAt = survivalAt.Add(100 * time.Millisecond)
				timeoutReferenceIdle(t, p.Ctx, max(0, time.Until(survivalAt)))
				showBefore := time.Now()
				newDeadline := chainBefore.Add(transactionTimeout)
				if showBefore.Before(survivalAt) || !showBefore.Before(newDeadline) {
					t.Fatalf("inconclusive survival window overrun: BEGIN bracket=[%s, %s], CHAIN bracket=[%s, %s], survival target=%s, SHOW start=%s, earliest chained deadline=%s",
						beginBefore, beginAfter, chainBefore, chainAfter, survivalAt, showBefore, newDeadline)
				}
				f.show("transaction_timeout", "6s")
				showAfter := time.Now()
				if !showAfter.Before(newDeadline) {
					t.Fatalf("inconclusive survival window overrun: BEGIN bracket=[%s, %s], CHAIN bracket=[%s, %s], survival target=%s, SHOW bracket=[%s, %s], earliest chained deadline=%s",
						beginBefore, beginAfter, chainBefore, chainAfter, survivalAt, showBefore, showAfter, newDeadline)
				}
				timeoutReferenceIdle(t, p.Ctx, max(0, time.Until(chainAfter.Add(transactionTimeout+100*time.Millisecond))))
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
		timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			f := newTimeoutReferenceFixture(t, p, b)
			const gid = "timeout_detached"
			fixture.PreparedReferenceCleanup(t, b, gid)
			f.set("transaction_timeout", "250ms")
			f.set("idle_session_timeout", "375ms")
			f.beginPrivate()
			f.exec(f.owner, "PREPARE TRANSACTION '"+gid+"'", 0)
			entry := fixture.PreparedReferenceOne(t, p, f.peer, gid)
			// A delayed idle callback may lose admission to a probe on a busy
			// runner. Give it another full idle interval, without querying the
			// table locked by the detached transaction and starting a lock wait.
			terminated := false
			for range 8 {
				timeoutReferenceIdle(t, p.Ctx, 500*time.Millisecond)
				var value int
				err := f.owner.QueryRowContext(p.Ctx, "SELECT 1").Scan(&value)
				if err != nil {
					f.diagnostic(err, "57P05")
					f.dead()
					terminated = true
					break
				}
				if value != 1 {
					t.Fatalf("idle probe returned %d, want 1", value)
				}
			}
			if !terminated {
				t.Fatal("origin did not terminate after eight idle intervals")
			}
			got := fixture.PreparedReferenceOne(t, p, f.peer, gid)
			expectedZone, expectedOffset := entry.Prepared.Zone()
			gotZone, gotOffset := got.Prepared.Zone()
			if got.Xid != entry.Xid || got.Gid != entry.Gid || got.Owner != entry.Owner || got.Database != entry.Database ||
				!got.Prepared.Equal(entry.Prepared) || gotZone != expectedZone || gotOffset != expectedOffset {
				t.Fatal("origin timeout changed detached entry", entry, got)
			}
			f.exec(f.peer, "BEGIN", 0)
			_, err := f.peer.ExecContext(p.Ctx, "LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE NOWAIT")
			fixture.WaitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "timeout_sentinel"`)
			f.exec(f.peer, "ROLLBACK", 0)
			f.exec(f.peer, "COMMIT PREPARED '"+gid+"'", 0)
			fixture.PreparedReferenceAbsent(t, p, f.peer, gid)
			f.rows(f.peer, 7, 8)
		})
	})
}

func TestReferenceTimeoutFatalCleanup(t *testing.T) {
	for _, code := range []string{"25P04", "25P03"} {
		t.Run(code, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				f.exec(f.owner, "CREATE TEMP TABLE timeout_owner_temp(v INT)", 0)
				f.exec(f.owner, "INSERT INTO timeout_owner_temp VALUES(11)", 1)
				f.exec(f.peer, "CREATE TEMP TABLE timeout_peer_temp(v INT)", 0)
				f.exec(f.peer, "INSERT INTO timeout_peer_temp VALUES(22)", 1)
				fixture.RunStep(t, p.Ctx, f.peer, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='timeout_owner_temp'", []string{"relname"}, []string{"NAME"}, [][]any{{"timeout_owner_temp"}}))
				held, err := f.owner.PrepareContext(p.Ctx, "SELECT v FROM timeout_owner_temp")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { held.Close() })
				op, pp := 0, 0
				if b.PG {
					op = fixture.LockPID(t, p.Ctx, f.owner)
					pp = fixture.LockPID(t, p.Ctx, f.peer)
				}
				setting := "transaction_timeout"
				if code == "25P03" {
					setting = "idle_in_transaction_session_timeout"
				}
				f.set(setting, "375ms")
				f.beginPrivate()
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := f.peer.QueryContext(ctx, "SELECT v FROM timeout_sentinel ORDER BY v")
					return fixture.RelationOutcome{Rows: r, Err: e}
				})
				if b.PG {
					fixture.LockWaitBehind(t, p.Ctx, pp, op, fixture.LockOID(t, p.Ctx, "public.timeout_sentinel"), "AccessShareLock")
				}
				timeoutReferenceIdle(t, p.Ctx, 500*time.Millisecond)
				f.fatalProbe(code)
				o := await()
				fixture.LimitLockValues(t, o, b.PG, 7)
				// The catalog witness tests actual TEMP disposal, not merely loss
				// of lookup through an already unusable physical owner.
				f.tempAbsent()
				fixture.RunStep(t, p.Ctx, f.peer, b.Recorder, fixture.Select("SELECT v FROM timeout_peer_temp", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
				rows, err := held.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("terminated handle published rows")
				}
				timeoutReferenceDeadError(t, err)
				fixture.RunStep(t, p.Ctx, f.peer, b.Recorder, localReferenceSelect(b.PG, "SHOW "+setting, setting, "0"))
				f.rows(f.peer, 7)
			})
		})
	}
}
