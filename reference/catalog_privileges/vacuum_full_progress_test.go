package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

var fullProgressColumns = []string{"pid", "datid", "datname", "relid", "command", "phase", "cluster_index_relid", "heap_tuples_scanned", "heap_tuples_written", "heap_blks_total", "heap_blks_scanned", "index_rebuild_count"}
var fullProgressTypes = []string{"INT4", "OID", "NAME", "OID", "TEXT", "TEXT", "OID", "INT8", "INT8", "INT8", "INT8", "INT8"}

func TestReferenceVacuumFullProgress(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		fullClusterDefinition(f)
		empty := fixture.Select("SELECT * FROM pg_stat_progress_cluster", fullProgressColumns, fullProgressTypes, nil)
		f.run(empty)
		vacuumRun(f.t, f.p, f.b, vacuumHeld(f.t, f.p, f.c, empty.Query), empty)
		fullActiveRewrite(f, false)
		fullTargetTurnover(f, false)
	}, "full_hidden_worker")
}

// This is actual index recipe execution parked on the canonical public
// advisory scheduler in both engines, never a fake progress worker or delay.
func fullActiveRewrite(f *fullVacuumCase, canceled bool) {
	f.t.Helper()
	f.run(fixture.Exec("CREATE TABLE full_active(id INT PRIMARY KEY)", 0))
	f.run(fixture.Exec("INSERT INTO full_active VALUES(1),(2),(3)", 3))
	f.run(fixture.Exec("CREATE TABLE full_active_anchor(id INT)", 0))
	f.run(fixture.Exec("INSERT INTO full_active_anchor VALUES(9)", 1))
	retained := vacuumConn(f.t, f.p, f.b)
	vacuumRun(f.t, f.p, f.b, retained, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	defer retained.ExecContext(f.p.Ctx, "ROLLBACK")
	// Polling for the active worker itself owns transient live tickets. Make
	// retirement deterministic with an independent, explicit old snapshot,
	// rather than assume observation cannot overlap the worker's capture.
	// Touch only the anchor: target AS would block the worker's AX.
	vacuumRows(f.t, f.p, f.b, retained, "full_active_anchor", 9)
	f.run(fixture.Exec("DELETE FROM full_active WHERE id=1", 1))
	f.run(fixture.Exec("CREATE FUNCTION full_active_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN PERFORM pg_advisory_xact_lock(760018); RETURN i; END $$", 0))
	f.run(fixture.Exec("CREATE INDEX full_active_ix ON full_active(full_active_key(id))", 0))
	f.run(fixture.Exec("CREATE ROLE full_hidden_worker", 0))
	holder, observer := vacuumConn(f.t, f.p, f.b), vacuumConn(f.t, f.p, f.b)
	pid, blocker := fixture.LockPID(f.t, f.p.Ctx, f.c), fixture.LockPID(f.t, f.p.Ctx, holder)
	before, ix := f.locator("full_active"), f.locator("full_active_ix")
	vacuumRun(f.t, f.p, f.b, holder, fixture.Select("SELECT pg_advisory_lock(760018)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
	locked := true
	defer func() {
		if locked {
			if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760018)"); err != nil {
				f.t.Error(err)
			}
		}
	}()
	cancel, join, finished := f.start("VACUUM FULL full_active")
	fullAwaitAdvisory(f, observer, pid, blocker, finished)
	var worker, datid, relation, cluster, scanned, written, total, blocks, indexes int64
	var database, command, phase string
	if err := observer.QueryRowContext(f.p.Ctx, "SELECT * FROM pg_stat_progress_cluster WHERE pid=$1", pid).Scan(&worker, &datid, &database, &relation, &command, &phase, &cluster, &scanned, &written, &total, &blocks, &indexes); err != nil {
		f.t.Fatal(err)
	}
	if worker != int64(pid) || datid <= 0 || database == "" || relation != before.oid || command != "VACUUM FULL" || phase != "rebuilding index" || cluster != 0 || scanned != 3 || written != 3 || total < blocks || blocks < 0 || indexes < 0 || indexes > 1 {
		f.t.Fatalf("active rewrite progress: %d %d %s %d %s %s %d %d %d %d %d %d", worker, datid, database, relation, command, phase, cluster, scanned, written, total, blocks, indexes)
	}
	if !f.b.PG && (total != 0 || blocks != 0) {
		f.t.Fatal("fabricated native heap work", scanned, total, blocks)
	}
	vacuumRun(f.t, f.p, f.b, observer, fixture.Select("SELECT count(*) FROM pg_stat_progress_vacuum WHERE pid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, pid))
	viewer := vacuumConn(f.t, f.p, f.b)
	vacuumRun(f.t, f.p, f.b, viewer, fixture.Exec("SET ROLE full_hidden_worker", 0))
	fullProgressMasked(f.t, viewer.QueryRowContext(f.p.Ctx, "SELECT * FROM pg_stat_progress_cluster WHERE pid=$1", pid))
	vacuumRun(f.t, f.p, f.b, viewer, fixture.Exec("RESET ROLE", 0))
	if _, err := observer.ExecContext(f.p.Ctx, "GRANT pg_read_all_stats TO full_hidden_worker"); err != nil {
		f.t.Fatal(err)
	}
	vacuumRun(f.t, f.p, f.b, viewer, fixture.Exec("SET ROLE full_hidden_worker", 0))
	vacuumRun(f.t, f.p, f.b, viewer, fixture.Select("SELECT command,relid::bigint AS relid FROM pg_stat_progress_cluster WHERE pid=$1", []string{"command", "relid"}, []string{"TEXT", "INT8"}, [][]any{{"VACUUM FULL", before.oid}}, pid))
	vacuumRun(f.t, f.p, f.b, viewer, fixture.Exec("RESET ROLE", 0))
	// The worker's granted AX blocks both read and write admission.
	for _, read := range []bool{true, false} {
		peer := vacuumConn(f.t, f.p, f.b)
		peerPID := fixture.LockPID(f.t, f.p.Ctx, peer)
		ctx, stop := context.WithCancel(f.p.Ctx)
		f.t.Cleanup(stop)
		finishedPeer := make(chan struct{})
		await := fixture.LockOperation(f.t, ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finishedPeer)
			var o fixture.RelationOutcome
			if read {
				o.Rows, o.Err = peer.QueryContext(ctx, "SELECT id FROM full_active")
			} else {
				o.Result, o.Err = peer.ExecContext(ctx, "INSERT INTO full_active VALUES(4)")
			}
			return o
		})
		mode := "AccessShareLock"
		if !read {
			mode = "RowExclusiveLock"
		}
		fixture.RelationLockPending(f.t, f.p, f.b, peerPID, pid, before.oid, mode, finishedPeer)
		if f.b.PG {
			var ok bool
			if err := observer.QueryRowContext(f.p.Ctx, "SELECT pg_cancel_backend($1)", peerPID).Scan(&ok); err != nil || !ok {
				f.t.Fatal(err)
			}
		} else {
			stop()
		}
		o := await()
		stop()
		if o.Rows != nil {
			o.Rows.Close()
		}
		if o.Err == nil {
			f.t.Fatal("blocked peer admitted during FULL")
		}
	}
	if canceled {
		fullCancel(f, observer, pid, cancel, join, "SQL statement \"SELECT pg_advisory_xact_lock(760018)\"\nPL/pgSQL function full_active_key(integer) line 1 at PERFORM")
		f.unchanged("full_active", before)
		f.unchanged("full_active_ix", ix)
	} else {
		if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760018)"); err != nil {
			f.t.Fatal(err)
		}
		locked = false
		o := join()
		fixture.DeleteReferenceCount(f.t, o.Result, o.Err, 0)
		f.rewritten("full_active", before)
		f.rewritten("full_active_ix", ix)
	}
	if locked {
		if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760018)"); err != nil {
			f.t.Fatal(err)
		}
		locked = false
	}
	// AX is now released, so target admission cannot obstruct the rewrite.
	// The retained owner independently explains why all three heads were copied;
	// current execution must still hide the ended one.
	vacuumRows(f.t, f.p, f.b, retained, "full_active", 1, 2, 3)
	vacuumRows(f.t, f.p, f.b, f.c, "full_active", 2, 3)
	vacuumRun(f.t, f.p, f.b, retained, fixture.Exec("ROLLBACK", 0))
	// Retry canceled work, or perform the next rewrite without the retained
	// snapshot. Native root tests separately prove exact quiescent retirement.
	retry, retryIndex := f.locator("full_active"), f.locator("full_active_ix")
	f.command(fixture.Exec("VACUUM FULL full_active", 0))
	f.rewritten("full_active", retry)
	f.rewritten("full_active_ix", retryIndex)
	vacuumRows(f.t, f.p, f.b, f.c, "full_active", 2, 3)
	f.emptyProgress()
	f.run(fixture.Exec("DROP ROLE full_hidden_worker", 0))
}

// Keep explicit nullable scanning of a redacted source separate from native
// progress ownership units; SQL NULL is not an invented zero work estimate.
func fullProgressMasked(t *testing.T, row *sql.Row) {
	t.Helper()
	var pid, datid int64
	var database string
	var relation, cluster sql.NullInt64
	var command, phase sql.NullString
	var counters [5]sql.NullInt64
	args := []any{&pid, &datid, &database, &relation, &command, &phase, &cluster}
	for i := range counters {
		args = append(args, &counters[i])
	}
	if err := row.Scan(args...); err != nil {
		t.Fatal(err)
	}
	if pid <= 0 || datid <= 0 || database == "" || relation.Valid || cluster.Valid || command.Valid || phase.Valid {
		t.Fatal("redacted identity", pid, datid, database, relation, command, phase, cluster)
	}
	for _, counter := range counters {
		if counter.Valid {
			t.Fatal("redacted work leaked", counter)
		}
	}
}
