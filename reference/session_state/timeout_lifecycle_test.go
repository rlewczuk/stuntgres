package reference_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimeoutChainPrepared(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "aborted-COMMIT", "aborted-ROLLBACK"} {
		t.Run(end, func(t *testing.T) {
			timeoutReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				f := newTimeoutReferenceFixture(t, p, b)
				for budget := 200 * time.Millisecond; budget <= 6400*time.Millisecond; budget *= 2 {
					f.requireAttemptTime(budget)
					value := fmt.Sprintf("%dms", budget.Milliseconds())
					localBudget := budget / 4
					localValue := fmt.Sprintf("%dms", localBudget.Milliseconds())
					identity := fixture.SettingsPhysical(t, f.owner)
					f.set("transaction_timeout", value)
					beginBefore := time.Now()
					err := f.preparationExec("BEGIN", 0)
					beginAfter := time.Now()
					if err == nil {
						err = f.preparationExec("LOCK TABLE timeout_sentinel IN ACCESS EXCLUSIVE MODE", 0)
					}
					if err == nil {
						err = f.preparationExec("INSERT INTO timeout_sentinel VALUES(8)", 1)
					}
					if err == nil {
						err = f.preparationExec("SET LOCAL transaction_timeout='"+localValue+"'", 0)
					}
					command := end
					if err == nil && (end == "aborted-COMMIT" || end == "aborted-ROLLBACK") {
						err = f.preparationExec("SELECT timeout_missing", 0)
						var state interface{ SQLState() string }
						if errors.As(err, &state) && state.SQLState() == "42703" {
							want := fixture.ControlsReferenceDiagnostic("42703", `column "timeout_missing" does not exist`)
							want.Position = "8"
							fixture.AssertDiagnostic(t, err, &want)
							err = nil
						} else if err == nil {
							t.Fatal("aborting CHAIN preparation unexpectedly succeeded")
						}
						command = "COMMIT"
						if end == "aborted-ROLLBACK" {
							command = "ROLLBACK"
						}
					}
					if err != nil {
						f.preparationExpired(err, beginBefore.Add(budget))
						f.rows(f.peer, 7)
						f.replaceTimeoutOwner()
						continue
					}
					timeoutReferenceUntil(t, p.Ctx, beginAfter.Add(budget/2))
					chainBefore := time.Now()
					if !chainBefore.Before(beginBefore.Add(budget)) && end != "aborted-COMMIT" && end != "aborted-ROLLBACK" {
						f.awaitTransactionFatal(p.Ctx)
						f.dead()
						f.rows(f.peer, 7)
						f.replaceTimeoutOwner()
						continue
					}
					err = f.preparationExec(command+" AND CHAIN", 0)
					chainAfter := time.Now()
					if err != nil {
						f.preparationExpired(err, beginBefore.Add(budget))
						f.rows(f.peer, 7)
						f.replaceTimeoutOwner()
						continue
					}
					err = f.preparationShow(value)
					if err != nil {
						f.preparationExpired(err, chainBefore.Add(budget))
						if end == "COMMIT" {
							f.rows(f.peer, 7, 8)
							f.exec(f.peer, "DELETE FROM timeout_sentinel WHERE v=8", 1)
						}
						f.rows(f.peer, 7)
						f.replaceTimeoutOwner()
						continue
					}
					// Crossing both old BEGIN and old LOCAL deadlines is the
					// discriminating survival witness, not an elapsed-delay guess.
					survivalAt := beginAfter.Add(budget)
					if localDeadline := chainAfter.Add(localBudget); localDeadline.After(survivalAt) {
						survivalAt = localDeadline
					}
					survivalAt = survivalAt.Add(time.Millisecond)
					timeoutReferenceUntil(t, p.Ctx, survivalAt)
					newEarliest := chainBefore.Add(budget)
					newLatest := chainAfter.Add(budget)
					missed := !time.Now().Before(newLatest)
					if !missed {
						err = f.preparationShow(value)
						if err != nil {
							if time.Now().Before(newLatest) {
								f.diagnostic(err, "25P04")
								t.Fatal("CHAIN failed its armed survival assertion")
							}
							f.preparationExpired(err, newEarliest)
							missed = true
						} else if !time.Now().Before(newEarliest) {
							if time.Now().Before(newLatest) {
								t.Fatal("CHAIN survival observation straddled new deadline bracket")
							}
							missed = true
						}
					}
					if err == nil {
						if !missed && fixture.SettingsPhysical(t, f.owner) != identity {
							t.Fatal("CHAIN replaced physical owner")
						}
						timeoutReferenceUntil(t, p.Ctx, newLatest)
						f.awaitTransactionFatal(p.Ctx)
						f.dead()
					}
					if end == "COMMIT" {
						f.rows(f.peer, 7, 8)
					} else {
						f.rows(f.peer, 7)
					}
					if !missed {
						return
					}
					if end == "COMMIT" {
						f.exec(f.peer, "DELETE FROM timeout_sentinel WHERE v=8", 1)
						f.rows(f.peer, 7)
					}
					f.replaceTimeoutOwner()
				}
				t.Fatal("adaptive CHAIN preparation produced no decisive survival witness")
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
				// The peer's successful read is the observable release condition.
				// Await it before probing the owner, rather than assuming timer
				// delivery and rollback completed during a fixed idle interval.
				o := await()
				fixture.LimitLockValues(t, o, b.PG, 7)
				f.fatalProbe(code)
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
