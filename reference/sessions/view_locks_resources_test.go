package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func viewLocksCall(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, want any) {
	t.Helper()
	var got any
	viewLocksScalar(t, p, x, q, &got)
	if want != nil && fixture.Normalize(got) != want {
		t.Fatal(q, got, want)
	}
}

func TestReferenceViewLocksAdvisory(t *testing.T) {
	viewLocksScenario(t, viewLocksAdvisory)
}

func viewLocksAdvisory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	a, aid := blockingConn(t, p, b)
	o, _ := blockingConn(t, p, b)
	db := viewLocksDatabase(t, p, o)
	for _, q := range []string{"SELECT pg_advisory_lock(-1::bigint)", "SELECT pg_advisory_lock(-1::bigint)", "SELECT pg_advisory_lock(-1,-1)", "SELECT pg_advisory_lock_shared(-1::bigint)"} {
		viewLocksCall(t, p, a, q, nil)
	}
	where := "pid=$1 AND locktype='advisory'"
	check := func(active bool) []viewLockRow {
		t.Helper()
		rows := viewLocksRead(t, p.Ctx, b, o, where, aid)
		if len(rows) != 3 {
			t.Fatal("counted advisory mode rows", rows)
		}
		modes := map[string]bool{}
		for _, r := range rows {
			viewLocksTarget(t, r, "advisory", db, 0)
			if !r.Granted || r.Class != (sql.NullInt64{Int64: 4294967295, Valid: true}) || r.Object != r.Class || !r.Subobject.Valid || r.Subobject.Int64 != 1 && r.Subobject.Int64 != 2 {
				t.Fatal("advisory identity", r)
			}
			modes[r.Mode+"/"+map[int64]string{1: "bigint", 2: "pair"}[r.Subobject.Int64]] = true
			if active {
				if r.Owner != viewLocksOwner(t, p, b, o, aid).Owner {
					t.Fatal("active advisory provenance", r)
				}
			} else if !strings.HasSuffix(r.Owner.String, "/0") {
				t.Fatal("idle advisory owner", r)
			}
		}
		if !modes["ExclusiveLock/bigint"] || !modes["ShareLock/bigint"] || !modes["ExclusiveLock/pair"] {
			t.Fatal("advisory modes", rows)
		}
		return rows
	}
	check(false)
	viewLocksCall(t, p, a, "SELECT pg_advisory_unlock(-1::bigint)", true)
	check(false)
	namespaceExec(t, p, b, a, "BEGIN")
	check(true)
	namespaceExec(t, p, b, a, "ROLLBACK")
	check(false)
	viewLocksCall(t, p, a, "SELECT pg_advisory_unlock(-1::bigint)", true)
	rows := viewLocksRead(t, p.Ctx, b, o, where, aid)
	if len(rows) != 2 {
		t.Fatal("final exclusive unlock", rows)
	}
	for _, r := range rows {
		if r.Subobject.Int64 == 1 && r.Mode == "ExclusiveLock" {
			t.Fatal("exclusive count remains", r)
		}
	}
	viewLocksCall(t, p, a, "SELECT pg_advisory_unlock_shared(-1::bigint)", true)
	viewLocksCall(t, p, a, "SELECT pg_advisory_unlock(-1,-1)", true)
	viewLocksEmpty(t, p, b, o, where, aid)
	viewLocksCall(t, p, a, "SELECT pg_advisory_lock(-1::bigint)", nil)
	if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
		t.Fatal(err)
	}
	viewLocksPoll(t, p, b, o, where, func(r []viewLockRow) bool { return len(r) == 0 }, aid)
}

func TestReferenceViewLocksTransactionOwnership(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_xid(v INT)", "BEGIN READ ONLY")
		vx := viewLocksOwner(t, p, b, o, aid)
		viewLocksEmpty(t, p, b, o, "pid=$1 AND locktype='transactionid'", aid)
		namespaceExec(t, p, b, a, "COMMIT", "BEGIN")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_xid VALUES(1)", 1))
		xid := viewLocksXID(t, p, b, o, aid)
		if xid.Owner != viewLocksOwner(t, p, b, o, aid).Owner || xid.Owner == vx.Owner {
			t.Fatal("transaction incarnation", vx, xid)
		}
		// Assign the top-level XID before entering a savepoint. PostgreSQL
		// exposes an additional real child XID during a subtransaction write;
		// native savepoints retain the top-level assignment without child XIDs.
		assertRootMembership := func(phase string) {
			t.Helper()
			rows := viewLocksRead(t, p.Ctx, b, o, "pid=$1 AND locktype='transactionid' AND granted", aid)
			matches := 0
			for _, row := range rows {
				if row.XID != xid.XID {
					continue // PostgreSQL may own additional real child XIDs.
				}
				matches++
				viewLocksTarget(t, row, "transactionid", 0, 0)
				if row != xid {
					t.Fatal(phase, "root ownership/mode/target changed", row, xid)
				}
			}
			if matches != 1 || !b.PG && len(rows) != 1 {
				t.Fatal(phase, "root membership or native child-XID fabrication", rows, xid)
			}
		}
		namespaceExec(t, p, b, a, "SAVEPOINT before_write")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_xid VALUES(2)", 1))
		assertRootMembership("active child")
		namespaceExec(t, p, b, a, "ROLLBACK TO before_write")
		assertRootMembership("after child rollback")
		remaining := fixture.Select("SELECT v FROM view_locks_xid ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, a, b.Recorder, remaining)
		namespaceExec(t, p, b, a, "COMMIT")
		viewLocksEmpty(t, p, b, o, "pid=$1 AND locktype IN ('transactionid','virtualxid')", aid)
		fixture.RunStep(t, p.Ctx, o, b.Recorder, remaining)
	})
}

func TestReferenceViewLocksCompletionWaits(t *testing.T) {
	t.Run("index_writer_VXID", func(t *testing.T) {
		viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, aid := blockingConn(t, p, b)
			w, wid := blockingConn(t, p, b)
			o, _ := blockingConn(t, p, b)
			namespaceExec(t, p, b, a, "CREATE TABLE view_locks_index(v INT)", "BEGIN")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_index VALUES(1)", 1))
			holder := viewLocksOwner(t, p, b, o, aid)
			viewLocksXID(t, p, b, o, aid) // Even an assigned writer completion waits on VXID.
			before := time.Now()
			await := blockingLock(t, p, w, "CREATE INDEX CONCURRENTLY view_locks_index_idx ON view_locks_index(v)", false)
			rows := viewLocksPoll(t, p, b, o, "pid=$1 AND NOT granted", func(rows []viewLockRow) bool { return viewLocksHasPending(rows, wid, "ShareLock") }, wid)
			r := viewLocksPending(t, rows, wid, "ShareLock", before)
			viewLocksTarget(t, r, "virtualxid", 0, 0)
			if r.VirtualXID != holder.VirtualXID {
				t.Fatal("completion target", r, holder)
			}
			namespaceExec(t, p, b, a, "COMMIT")
			blockingLockDone(t, await)
			viewLocksEmpty(t, p, b, o, "pid=$1 AND mode='ShareLock' AND locktype IN ('virtualxid','transactionid')", wid)
		})
	})
	for _, prepareWhileWaiting := range []bool{false, true} {
		name := "index_prepared_writer_XID"
		if prepareWhileWaiting {
			name = "index_live_writer_PREPARE"
		}
		t.Run(name, func(t *testing.T) {
			viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				viewLocksPreparedIndexCompletion(t, p, b, prepareWhileWaiting)
			})
		})
	}
	t.Run("exclusion_XID", func(t *testing.T) {
		viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, aid := blockingConn(t, p, b)
			w, wid := blockingConn(t, p, b)
			o, _ := blockingConn(t, p, b)
			namespaceExec(t, p, b, a, "CREATE TABLE view_locks_exclusion(v INT, CONSTRAINT view_locks_exclusion_equal EXCLUDE USING btree (v WITH =))", "BEGIN")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_exclusion VALUES(1)", 1))
			holder := viewLocksXID(t, p, b, o, aid)
			namespaceExec(t, p, b, w, "BEGIN")
			before := time.Now()
			await := blockingLock(t, p, w, "INSERT INTO view_locks_exclusion VALUES(1)", false)
			rows := viewLocksPoll(t, p, b, o, "pid=$1 AND NOT granted", func(rows []viewLockRow) bool { return viewLocksHasPending(rows, wid, "ShareLock") }, wid)
			r := viewLocksPending(t, rows, wid, "ShareLock", before)
			viewLocksTarget(t, r, "transactionid", 0, 0)
			if r.XID != holder.XID {
				t.Fatal("exclusion completion target", r, holder)
			}
			namespaceExec(t, p, b, a, "ROLLBACK")
			viewLocksAffected(t, await(), 1)
			viewLocksEmpty(t, p, b, o, "pid=$1 AND mode='ShareLock' AND locktype='transactionid'", wid)
			namespaceExec(t, p, b, w, "ROLLBACK")
		})
	})
}

func viewLocksAffected(t *testing.T, o fixture.RelationOutcome, want int64) {
	t.Helper()
	if o.Err != nil || o.Result == nil {
		t.Fatal("worker result", o.Err)
	}
	if n, err := o.Result.RowsAffected(); err != nil || n != want {
		t.Fatal("worker affected", n, want, err)
	}
}

func TestReferenceViewLocksRowKeyWaits(t *testing.T) {
	for _, kind := range []string{"row", "unique_commit", "unique_rollback"} {
		t.Run(kind, func(t *testing.T) {
			viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { viewLocksRowKey(t, p, b, kind) })
		})
	}
}

func viewLocksRowKey(t *testing.T, p *fixture.Pair, b fixture.Backend, kind string) {
	t.Helper()
	a, aid := blockingConn(t, p, b)
	w, wid := blockingConn(t, p, b)
	o, _ := blockingConn(t, p, b)
	namespaceExec(t, p, b, a, "CREATE TABLE view_locks_rowkey(id INT CONSTRAINT view_locks_rowkey_pk PRIMARY KEY,v INT)")
	if kind == "row" {
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_rowkey VALUES(1,1)", 1))
	}
	namespaceExec(t, p, b, a, "BEGIN")
	hold := "INSERT INTO view_locks_rowkey VALUES(1,1)"
	q := "INSERT INTO view_locks_rowkey VALUES(1,2)"
	if kind == "row" {
		hold = "UPDATE view_locks_rowkey SET v=2 WHERE id=1"
		q = "UPDATE view_locks_rowkey SET v=3 WHERE id=1"
	}
	fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec(hold, 1))
	xid := viewLocksXID(t, p, b, o, aid)
	viewLocksEmpty(t, p, b, o, "pid=$1 AND locktype IN ('tuple','spectoken')", aid)
	namespaceExec(t, p, b, w, "BEGIN")
	before := time.Now()
	await := blockingLock(t, p, w, q, false)
	rows := viewLocksPoll(t, p, b, o, "pid=$1 AND NOT granted", func(rows []viewLockRow) bool { return viewLocksHasPending(rows, wid, "ShareLock") }, wid)
	r := viewLocksPending(t, rows, wid, "ShareLock", before)
	viewLocksTarget(t, r, "transactionid", 0, 0)
	if r.XID != xid.XID {
		t.Fatal("row/key dependency not holder XID", r, xid)
	}
	if kind == "unique_commit" {
		namespaceExec(t, p, b, a, "COMMIT")
		o := await()
		if o.Result != nil {
			t.Fatal("unique failure published result")
		}
		want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "view_locks_rowkey_pk"`)
		want.Detail = "Key (id)=(1) already exists."
		want.Schema = "public"
		want.Table = "view_locks_rowkey"
		want.Constraint = "view_locks_rowkey_pk"
		fixture.AssertDiagnostic(t, o.Err, &want)
	} else {
		namespaceExec(t, p, b, a, "ROLLBACK")
		viewLocksAffected(t, await(), 1)
	}
	viewLocksEmpty(t, p, b, o, "pid=$1 AND NOT granted", wid)
	namespaceExec(t, p, b, w, "ROLLBACK")
}

func TestReferenceViewLocksObject(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		w, wid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TYPE view_locks_enum AS ENUM ('old')")
		oid := viewLocksOID(t, p, o, "SELECT 'public.view_locks_enum'::regtype::oid")
		db := viewLocksDatabase(t, p, o)
		namespaceExec(t, p, b, a, "BEGIN", "ALTER TYPE view_locks_enum ADD VALUE 'first'")
		namespaceExec(t, p, b, w, "BEGIN")
		before := time.Now()
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			r, err := w.ExecContext(ctx, "ALTER TYPE view_locks_enum ADD VALUE 'second'")
			return fixture.RelationOutcome{Result: r, Err: err}
		})
		where := "locktype='object' AND classid=1247 AND objid=$1"
		rows := viewLocksPoll(t, p, b, o, where, func(rows []viewLockRow) bool {
			return len(rows) == 2 && viewLocksHasPending(rows, wid, "ExclusiveLock")
		}, oid)
		viewLocksPending(t, rows, wid, "ExclusiveLock", before)
		for _, r := range rows {
			viewLocksTarget(t, r, "object", db, 0)
			if r.Class != (sql.NullInt64{Int64: 1247, Valid: true}) || r.Object != (sql.NullInt64{Int64: oid, Valid: true}) || r.Subobject != (sql.NullInt64{Valid: true}) || r.Mode != "ExclusiveLock" || !r.PID.Valid || r.PID.Int64 != int64(aid) && r.PID.Int64 != int64(wid) {
				t.Fatal("type target", r)
			}
		}
		namespaceExec(t, p, b, a, "COMMIT")
		blockingLockDone(t, await)
		r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, where, oid))
		if r.PID.Int64 != int64(wid) || !r.Granted {
			t.Fatal("type grant", r)
		}
		namespaceExec(t, p, b, w, "COMMIT")
		viewLocksEmpty(t, p, b, o, where, oid)
	})
}

func viewLocksPreparedIndexCompletion(t *testing.T, p *fixture.Pair, b fixture.Backend, prepareWhileWaiting bool) {
	t.Helper()
	a, aid := blockingConn(t, p, b)
	w, wid := blockingConn(t, p, b)
	o, _ := blockingConn(t, p, b)
	namespaceExec(t, p, b, a, "CREATE TABLE view_locks_prepared_index(v INT)", "BEGIN")
	fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO view_locks_prepared_index VALUES(1)", 1))
	holder := viewLocksOwner(t, p, b, o, aid)
	assigned := viewLocksXID(t, p, b, o, aid)
	// Register the GID cleanup before the worker, including when PREPARE
	// itself happens later. Failure paths cancel/join before prepared cleanup.
	fixture.PreparedReferenceCleanup(t, b, "view_locks_index_writer")
	prepare := func() { namespaceExec(t, p, b, a, "PREPARE TRANSACTION 'view_locks_index_writer'") }
	if !prepareWhileWaiting {
		prepare()
	}
	before := time.Now()
	await := blockingLock(t, p, w, "CREATE INDEX CONCURRENTLY view_locks_prepared_index_idx ON view_locks_prepared_index(v)", false)
	var first viewLockRow
	var builder viewLockRow
	if prepareWhileWaiting {
		rows := viewLocksPoll(t, p, b, o, "pid=$1 AND NOT granted", func(rows []viewLockRow) bool {
			return viewLocksHasPending(rows, wid, "ShareLock")
		}, wid)
		first = viewLocksPending(t, rows, wid, "ShareLock", before)
		viewLocksTarget(t, first, "virtualxid", 0, 0)
		if first.VirtualXID != holder.VirtualXID {
			t.Fatal("live writer completion target", first, holder)
		}
		builder = viewLocksOwner(t, p, b, o, wid)
		prepare()
	}
	var xid string
	if err := o.QueryRowContext(p.Ctx, "SELECT transaction FROM pg_prepared_xacts WHERE gid='view_locks_index_writer'").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	if xid != assigned.XID.String {
		t.Fatal("independent prepared XID", xid, assigned)
	}
	viewLocksEmpty(t, p, b, o, "locktype='virtualxid' AND virtualxid=$1", holder.VirtualXID.String)
	prepared := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, "locktype='transactionid' AND transactionid=$1::xid AND granted", xid))
	if prepared.PID.Valid || prepared.Owner != holder.Owner {
		t.Fatal("prepared completion ownership", prepared)
	}
	rows := viewLocksPoll(t, p, b, o, "pid=$1 AND NOT granted", func(rows []viewLockRow) bool {
		for _, row := range rows {
			if row.Kind == "transactionid" && row.XID.String == xid && viewLocksHasPending([]viewLockRow{row}, wid, "ShareLock") {
				return true
			}
		}
		return false
	}, wid)
	pending := viewLocksPending(t, rows, wid, "ShareLock", before)
	viewLocksTarget(t, pending, "transactionid", 0, 0)
	if pending.XID.String != xid {
		t.Fatal("prepared writer completion invented retired VXID", pending, xid)
	}
	if prepareWhileWaiting {
		if current := viewLocksOwner(t, p, b, o, wid); current.Owner != builder.Owner || pending.Owner != builder.Owner {
			t.Fatal("PREPARE restarted the waiting builder", first, pending, builder, current)
		}
		// Native changes only observational target metadata on the same
		// journaled request. PostgreSQL may requeue under a different lock tag;
		// its bounded waitstart is not assumed equal to the original value.
		if !b.PG && !pending.Wait.Time.Equal(first.Wait.Time) {
			t.Fatal("native retarget reset original enqueue timestamp", first, pending)
		}
	}
	namespaceExec(t, p, b, o, "COMMIT PREPARED 'view_locks_index_writer'")
	blockingLockDone(t, await)
	viewLocksEmpty(t, p, b, o, "pid=$1 AND mode='ShareLock' AND locktype IN ('virtualxid','transactionid')", wid)
	viewLocksEmpty(t, p, b, o, "virtualtransaction=$1", holder.Owner.String)
}
