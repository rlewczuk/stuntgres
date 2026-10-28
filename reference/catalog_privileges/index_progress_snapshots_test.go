package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCreateIndexProgressOldSnapshots(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "success"
		if canceled {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			databases := []string{"index_progress_snapshot_observer"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				control := vacuumConn(t, p, b)
				copyProgressExec(t, p.Ctx, control, "CREATE DATABASE index_progress_snapshot_observer")
				observer := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "index_progress_snapshot_observer", ""))
				worker := fixture.ConcurrentIndexConn(t, p, b)
				holders := []*sql.Conn{fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)}
				copyProgressExec(t, p.Ctx, control, "CREATE TABLE index_progress_snapshot_work(n INT)")
				copyProgressExec(t, p.Ctx, control, "INSERT INTO index_progress_snapshot_work VALUES(1),(1),(2),(NULL)")
				copyProgressExec(t, p.Ctx, control, "CREATE TABLE index_progress_snapshot_unrelated(n INT)")
				copyProgressExec(t, p.Ctx, control, "INSERT INTO index_progress_snapshot_unrelated VALUES(8)")
				id := copyProgressIdentify(t, p, worker, "index_progress_snapshot_work")
				observerID := copyProgressIdentify(t, p, observer, "")
				if observerID.datid == id.datid || observerID.database == id.database {
					t.Fatalf("monitor must be outside the snapshot cohort's database: worker=%+v observer=%+v", id, observerID)
				}
				var holderIDs [2]int64
				for i, c := range holders {
					holderIDs[i] = copyProgressIdentify(t, p, c, "").pid
					copyProgressExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM index_progress_snapshot_unrelated ORDER BY n", 8)
				}
				run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_snapshot_ix ON index_progress_snapshot_work(n)", !canceled)
				first := indexProgressAwait(t, p, observer, id.pid, "waiting for old snapshots", 0)
				indexProgressVisible(t, first, id, "CREATE INDEX CONCURRENTLY")
				select {
				case err := <-run.Done:
					t.Fatalf("worker ended before old-snapshot observation: %v", err)
				default:
				}
				if first.counters[0].Int64 != 2 {
					t.Fatalf("real frozen old-snapshot cohort: %+v", first)
				}
				var indexOID int64
				if err := control.QueryRowContext(p.Ctx, "SELECT 'index_progress_snapshot_ix'::regclass::oid::bigint").Scan(&indexOID); err != nil {
					t.Fatal(err)
				}
				if first.index.Int64 != indexOID {
					t.Fatalf("old-snapshot index identity: %+v want OID %d", first, indexOID)
				}
				current := -1
				for i, pid := range holderIDs {
					if pid == first.counters[2].Int64 {
						current = i
					}
				}
				if current < 0 {
					t.Fatalf("snapshot current PID is not an actual holder: %+v holders=%v", first, holderIDs)
				}
				// Only the reported current owner leaves. The other frozen member
				// remains eligible, so the original second ordinal must be reached.
				copyProgressExec(t, p.Ctx, holders[current], "COMMIT")
				second := indexProgressAwait(t, p, observer, id.pid, "waiting for old snapshots", 1, holderIDs[1-current])
				indexProgressVisible(t, second, id, "CREATE INDEX CONCURRENTLY")
				if second.index.Int64 != indexOID || second.counters[0].Int64 != 2 || second.counters[2].Int64 != holderIDs[1-current] {
					t.Fatalf("exact old-snapshot completed ordinal/PID: first=%+v second=%+v holders=%v", first, second, holderIDs)
				}
				select {
				case err := <-run.Done:
					t.Fatalf("worker ended while final old snapshot remains: %v", err)
				default:
				}
				if canceled {
					indexProgressCancel(t, p, b, observer, id.pid, run.Cancel, func() error { return run.Join(t, p) })
					state, found := fixture.ConcurrentIndexState(t, p, b, "index_progress_snapshot_ix")
					if !found || !state.Ready || state.Valid || !state.Live {
						t.Fatalf("canceled old-snapshot wait must retain ready invalid index: %+v found=%v", state, found)
					}
					copyProgressExec(t, p.Ctx, holders[1-current], "ROLLBACK")
					copyProgressExec(t, p.Ctx, control, "DROP INDEX index_progress_snapshot_ix")
					if _, found := fixture.ConcurrentIndexState(t, p, b, "index_progress_snapshot_ix"); found {
						t.Fatal("canceled index cleanup retained catalog entry")
					}
				} else {
					copyProgressExec(t, p.Ctx, holders[1-current], "COMMIT")
					if err := run.Join(t, p); err != nil {
						t.Fatal(err)
					}
					fixture.ConcurrentIndexWait(t, p, b, "index_progress_snapshot_ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				}
				indexProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, control, fixture.Select("SELECT n FROM index_progress_snapshot_work ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}))
				fixture.ConcurrentIndexRows(t, p, b, control, "SELECT n FROM index_progress_snapshot_unrelated ORDER BY n", 8)
			})
		})
	}
}
