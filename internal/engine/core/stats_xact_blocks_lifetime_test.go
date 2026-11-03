package core_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestStatsXactBlocksPendingLifetime(t *testing.T) {
	for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("throttled_"+terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE block_life(k integer)")
				enginetest.Run(t, e, "INSERT INTO block_life VALUES(1)")
				s := enginetest.StartSession(t, e, nil)
				ref := enginetest.RoleTableRef(t, e, "block_life")
				statsFunctionExec(t, s, "SELECT k FROM block_life")
				blocksUnitPublished(t, e, ref, 1)
				statsFunctionExec(t, s, "BEGIN", "SELECT k FROM block_life", terminal)
				blocksUnitPublished(t, e, ref, 1)
				statsFunctionExec(t, s, "BEGIN")
				blocksUnitPending(t, e, s, ref, 1)
				statsFunctionExec(t, s, "SAVEPOINT block_mark")
				resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
				blocksUnitPending(t, e, s, ref, 1)
				if err := statsFunctionSQL(t, s, "SELECT k/(k-k) FROM block_life"); err == nil {
					t.Fatal("real post-open row error missing")
				}
				statsFunctionExec(t, s, "ROLLBACK TO block_mark")
				blocksUnitPending(t, e, s, ref, 2)
				// These are virtual clock advances inside synctest, not wall waits.
				time.Sleep(time.Second)
				synctest.Wait()
				blocksUnitPublished(t, e, ref, 1)
				blocksUnitPending(t, e, s, ref, 2)
				statsFunctionExec(t, s, "ROLLBACK")
				blocksUnitPublished(t, e, ref, 3)
				statsFunctionExec(t, s, "BEGIN")
				blocksUnitPending(t, e, s, ref, 0)
				statsFunctionExec(t, s, "ROLLBACK")
				time.Sleep(10 * time.Second)
				synctest.Wait()
				blocksUnitPublished(t, e, ref, 3)
			})
		})
	}
	for _, boundary := range []string{"busy", "retained", "aborted", "terminal"} {
		t.Run("publication_authority_"+boundary, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE block_gate(k integer)")
				enginetest.Run(t, e, "INSERT INTO block_gate VALUES(1)")
				s := enginetest.StartSession(t, e, nil)
				ref := enginetest.RoleTableRef(t, e, "block_gate")
				statsFunctionExec(t, s, "SELECT k FROM block_gate", "SELECT k FROM block_gate")
				blocksUnitPublished(t, e, ref, 1)
				blocksUnitPending(t, e, s, ref, 1)
				switch boundary {
				case "busy":
					op, err := s.AcquireActivity(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					blocksUnitPublished(t, e, ref, 1)
					op.Done()
				case "retained":
					capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_gate")
					time.Sleep(10 * time.Second)
					synctest.Wait()
					blocksUnitPublished(t, e, ref, 1)
					blocksUnitScope(t, e, capture.scope, ref, 1)
					capture.close(t)
					// Ordinary acquire is not a SQL ready boundary; explicitly
					// release an activity owner after the retained scope ends.
					op, err := s.AcquireActivity(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					op.Done()
				case "aborted":
					statsFunctionExec(t, s, "BEGIN")
					if err := statsFunctionSQL(t, s, "SELECT block_missing FROM block_gate"); err == nil {
						t.Fatal("missing failed statement")
					}
					time.Sleep(10 * time.Second)
					synctest.Wait()
					blocksUnitPublished(t, e, ref, 1)
					statsFunctionExec(t, s, "ROLLBACK")
				case "terminal":
					s.RequestEnd(session.CleanClose)
					if err := s.WaitEnd(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				blocksUnitPublished(t, e, ref, 2)
			})
		})
	}
	t.Run("prepare_does_not_transfer_backend_reads", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE block_prepared(k integer)")
			enginetest.Run(t, e, "INSERT INTO block_prepared VALUES(1)")
			preparer, finisher := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			ref := enginetest.RoleTableRef(t, e, "block_prepared")
			statsFunctionExec(t, preparer, "SELECT k FROM block_prepared", "BEGIN", "SELECT k FROM block_prepared", "PREPARE TRANSACTION 'block_pending_owner'")
			prepared := true
			defer func() {
				if prepared {
					statsFunctionSQL(t, finisher, "ROLLBACK PREPARED 'block_pending_owner'")
				}
			}()
			blocksUnitPending(t, e, preparer, ref, 1)
			blocksUnitPending(t, e, finisher, ref, 0)
			statsFunctionExec(t, finisher, "COMMIT PREPARED 'block_pending_owner'")
			prepared = false
			blocksUnitPending(t, e, finisher, ref, 0)
			blocksUnitPending(t, e, preparer, ref, 1)
			blocksUnitPublished(t, e, ref, 1)
			statsFunctionRetire(t, preparer)
			blocksUnitPublished(t, e, ref, 2)
		})
	})
}
