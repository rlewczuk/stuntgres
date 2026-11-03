package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

func TestStatsXactBlocksIdentity(t *testing.T) {
	for _, outcome := range []string{"private_abort", "savepoint_discard", "create_drop", "drop_commit", "drop_abort", "recreate"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			private := outcome == "private_abort" || outcome == "savepoint_discard" || outcome == "create_drop"
			if !private {
				statsFunctionExec(t, s, "CREATE TABLE block_identity(k integer)", "INSERT INTO block_identity VALUES(1)")
			}
			statsFunctionExec(t, s, "BEGIN")
			if outcome == "savepoint_discard" {
				statsFunctionExec(t, s, "SAVEPOINT block_birth")
			}
			if private {
				statsFunctionExec(t, s, "CREATE TABLE block_identity(k integer)", "INSERT INTO block_identity VALUES(1)")
			}
			statsFunctionExec(t, s, "SELECT k FROM block_identity")
			e.Mu.Lock()
			read := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			ref := enginetest.CatalogRef(t, e, read, "block_identity")
			key := statistics.Key{Object: ref, Category: statistics.Relation}
			blocksUnitPending(t, e, s, ref, 1)
			var late statistics.Writer
			late.Add(&e.Statistics, key, statistics.Fact{BlocksFetched: 17, BlocksHit: 17})
			switch outcome {
			case "private_abort":
				statsFunctionExec(t, s, "ROLLBACK")
			case "savepoint_discard":
				statsFunctionExec(t, s, "ROLLBACK TO block_birth")
				blocksUnitPending(t, e, s, ref, 0)
				statsFunctionExec(t, s, "COMMIT")
			case "create_drop", "drop_commit", "recreate":
				statsFunctionExec(t, s, "DROP TABLE block_identity", "COMMIT")
			case "drop_abort":
				statsFunctionExec(t, s, "SAVEPOINT before_drop", "DROP TABLE block_identity", "ROLLBACK TO before_drop")
				blocksUnitPending(t, e, s, ref, 1)
				statsFunctionExec(t, s, "ROLLBACK")
			}
			if outcome == "drop_abort" {
				statsFunctionRetire(t, s)
				blocksUnitPublished(t, e, ref, 1)
				if e.CatalogRead().Get(ref.Address) == nil {
					t.Fatal("aborted drop lost relation identity")
				}
				return
			}
			late.Flush(&e.Statistics, time.Now(), true)
			late.Add(&e.Statistics, key, statistics.Fact{BlocksFetched: 19, BlocksHit: 19})
			late.Flush(&e.Statistics, time.Now(), true)
			blocksUnitPublished(t, e, ref, 0)
			blocksUnitPending(t, e, s, ref, 0)
			if e.CatalogRead().Get(ref.Address) != nil {
				t.Fatal("discarded identity remains installed")
			}
			if outcome == "recreate" {
				statsFunctionExec(t, s, "CREATE TABLE block_identity(k integer)", "INSERT INTO block_identity VALUES(9)", "BEGIN", "SELECT k FROM block_identity")
				next := enginetest.RoleTableRef(t, e, "block_identity")
				if next.Address == ref.Address {
					t.Fatal("recreate reused retired identity")
				}
				blocksUnitPending(t, e, s, next, 1)
				blocksUnitPending(t, e, s, ref, 0)
				statsFunctionExec(t, s, "ROLLBACK")
			}
		})
	}
	t.Run("statistics_generation_rejects_captured_event", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE block_generation(k integer)")
		enginetest.Run(t, e, "INSERT INTO block_generation VALUES(1)")
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "BEGIN")
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_generation")
		_, stage := blocksUnitOpen(t, capture)
		ref := capture.source.Ref
		blocksUnitScope(t, e, capture.scope, ref, 1)
		e.Statistics.Reset(time.Now())
		blocksUnitScope(t, e, capture.scope, ref, 0)
		// This is the same still-admitted real source, not a fabricated Add.
		capture.source.ReadLease.Open()
		blocksUnitScope(t, e, capture.scope, ref, 0)
		stage.Close()
		capture.close(t)
		fresh := bufferUnitCapture(t, e, s, "SELECT k FROM block_generation")
		_, freshStage := blocksUnitOpen(t, fresh)
		blocksUnitScope(t, e, fresh.scope, ref, 1)
		freshStage.Close()
		fresh.close(t)
		statsFunctionExec(t, s, "ROLLBACK")
	})
	t.Run("readmitted_stale_storage_cannot_charge_successor", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE block_storage(k integer)")
		enginetest.Run(t, e, "INSERT INTO block_storage VALUES(1)")
		s := enginetest.StartSession(t, e, nil)
		old := bufferUnitCapture(t, e, s, "SELECT k FROM block_storage")
		lease, authority := old.source.ReadLease, core.ScopeAuthority(old.scope)
		ref := old.source.Ref
		old.close(t)
		statsFunctionExec(t, s, "TRUNCATE block_storage", "INSERT INTO block_storage VALUES(9)", "BEGIN")
		fresh := bufferUnitCapture(t, e, s, "SELECT k FROM block_storage")
		blocksUnitScope(t, e, fresh.scope, ref, 0)
		// Exercise the same current-host rebinding used by retained producers,
		// but deliberately keep the actual predecessor's storage capture.
		authority.Host = core.HostOf(fresh.scope)
		lease.Open()
		lease.Close()
		authority.Host = nil
		blocksUnitScope(t, e, fresh.scope, ref, 0)
		_, stage := blocksUnitOpen(t, fresh)
		blocksUnitScope(t, e, fresh.scope, ref, 1)
		stage.Close()
		fresh.close(t)
		statsFunctionExec(t, s, "ROLLBACK")
	})
	for _, control := range []string{"reset", "restore"} {
		t.Run("held_rows_"+control, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE block_control(k integer)")
				enginetest.Run(t, e, "INSERT INTO block_control VALUES(1),(2)")
				s := enginetest.StartSession(t, e, nil)
				ref := enginetest.RoleTableRef(t, e, "block_control")
				var dump bytes.Buffer
				if err := engine.Dump(context.Background(), e, &dump); err != nil {
					t.Fatal(err)
				}
				statsFunctionExec(t, s, "SELECT k FROM block_control")
				retained := enginetest.SessionSQL(t, s, "SELECT k FROM block_control ORDER BY k")
				blocksUnitPending(t, e, s, ref, 1)
				var late statistics.Writer
				late.Add(&e.Statistics, statistics.Key{Object: ref}, statistics.Fact{BlocksFetched: 23, BlocksHit: 23})
				var err error
				if control == "reset" {
					err = engine.Reset(context.Background(), e)
				} else {
					err = engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := enginetest.Tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
					t.Fatal("replacement mutated held rows", got)
				}
				retained.Rows.Close()
				late.Flush(&e.Statistics, time.Now(), true)
				blocksUnitPublished(t, e, ref, 0)
				blocksUnitPending(t, e, s, ref, 0)
				if control == "restore" {
					next := enginetest.RoleTableRef(t, e, "block_control")
					if next.Epoch == ref.Epoch || next.Domain() != ref.Domain() {
						t.Fatal("replacement identity fence", ref, next)
					}
					statsFunctionExec(t, s, "BEGIN", "SELECT k FROM block_control")
					blocksUnitPending(t, e, s, next, 1)
					blocksUnitPublished(t, e, ref, 0)
					statsFunctionExec(t, s, "ROLLBACK")
				}
			})
		})
	}
}
