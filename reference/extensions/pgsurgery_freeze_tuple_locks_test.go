package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

// These are leaves of the existing Concurrency family, not backend-specific
// scenarios. The first three operations are prepared before repair and admitted
// afterward with an unrelated waiter still live; the fourth follows genuine
// inherited lock ownership across two later normal successors.
func freezePostrepairLockCases(t *testing.T) {
	for _, operation := range []string{"nowait", "update", "delete", "successor-inherited"} {
		t.Run("postrepair-"+operation, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if operation == "successor-inherited" {
					freezePostrepairInheritedLocks(t, p, b, c)
				} else {
					freezePostrepairLocks(t, p, b, c, operation)
				}
			})
		})
	}
}

type freezeTupleOwner struct {
	xid string
	pid int
}

// Obtain the actual assigned transaction identity independently of the tuple
// header and pgrowlocks projection being tested; then assert its public lock.
func freezeAssignedOwner(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) freezeTupleOwner {
	t.Helper()
	var full string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&full); err != nil {
		t.Fatal(err)
	}
	xid, err := strconv.ParseUint(full, 10, 64)
	if err != nil || uint32(xid) < 3 {
		t.Fatalf("invalid assigned full transaction ID %q: %v", full, err)
	}
	owner := freezeTupleOwner{strconv.FormatUint(uint64(uint32(xid)), 10), fixture.LockPID(t, p.Ctx, c)}
	freezeBoth(t, p, b, c, fixture.Select("SELECT transactionid,pid FROM pg_locks WHERE locktype='transactionid' AND transactionid=$1::xid AND mode='ExclusiveLock' AND granted", []string{"transactionid", "pid"}, []string{"XID", "INT4"}, [][]any{{owner.xid, int64(owner.pid)}}, owner.xid))
	return owner
}

// All six released OUT fields, including empty-result metadata, are asserted
// through the installed public extension. Generated TIDs/XIDs/PIDs are related
// to actual captured rows and owners, never assumed equal between backends.
func freezeTupleProjection(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, relation, tid string, owner *freezeTupleOwner, mode string) {
	t.Helper()
	var expected [][]any
	if owner != nil {
		modes := "{\"" + mode + "\"}"
		if mode == "Update" {
			modes = "{Update}"
		}
		expected = [][]any{{tid, owner.xid, false, "{" + owner.xid + "}", modes, fmt.Sprintf("{%d}", owner.pid)}}
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.pgrowlocks($1::text) WHERE locked_row=$2::tid", []string{"locked_row", "locker", "multi", "xids", "modes", "pids"}, []string{"TID", "XID", "BOOL", "_XID", "_TEXT", "_INT4"}, expected, relation, tid))
}

func freezeUnrelatedHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, sentinel freezeHeader, holder freezeTupleOwner) {
	t.Helper()
	locked := sentinel
	locked.xmax = holder.xid
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,ctid,xmin,xmax FROM fs_part.sentinel ORDER BY k,v", []string{"k", "v", "ctid", "xmin", "xmax"}, []string{"INT4", "TEXT", "TID", "XID", "XID"}, [][]any{{locked.k, locked.v, locked.tid, locked.xmin, locked.xmax}}))
	freezeTupleProjection(t, p, b, c, "fs_part.sentinel", sentinel.tid, &holder, "For Update")
	freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=$1 AND granted AND ((locktype='relation' AND relation='fs_part.unrelated'::regclass AND mode='AccessExclusiveLock') OR locktype='advisory')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}, holder.pid))
	freezeRun(t, p, b, c, fixture.Select("SELECT pg_try_advisory_xact_lock(987654322::bigint) AS acquired", []string{"acquired"}, []string{"BOOL"}, [][]any{{false}}))
	freezeBoth(t, p, b, c, freezeError("SELECT k FROM fs_part.sentinel WHERE ctid=$1::tid FOR UPDATE NOWAIT", "55P03", "could not obtain lock on row in relation \"sentinel\"", sentinel.tid))
	freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
	freezeRun(t, p, b, c, fixture.ImplicitError("LOCK TABLE fs_part.unrelated IN ACCESS SHARE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", "could not obtain lock on relation \"fs_part.unrelated\"")))
	freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
}

func freezePostrepairLocks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, operation string) {
	t.Helper()
	freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION IF EXISTS pgrowlocks CASCADE", 0))
	freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pgrowlocks SCHEMA fs_part", 0))
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS pgrowlocks CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
	// The real UPDATE waiter must pin a different relation's page: surgery
	// needs a cleanup lock on A's page, independently of tuple arbitration.
	freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.sentinel(k integer,v text)", 0))
	freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.sentinel SET(autovacuum_enabled=false)", 0))
	sentinel := freezeOne(t, p, c, "INSERT INTO fs_part.sentinel VALUES(2,'B') RETURNING k,v,ctid,xmin,xmax")
	freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.unrelated(k integer)", 0))
	holder, writer, waiter := freezeConn(t, p, b), freezeConn(t, p, b), freezeConn(t, p, b)
	defer holder.Close()
	defer writer.Close()
	defer waiter.Close()
	defer holder.ExecContext(p.Ctx, "ROLLBACK")
	defer writer.ExecContext(p.Ctx, "ROLLBACK")

	query := "SELECT k FROM fs_part.heap WHERE ctid=$1::tid FOR UPDATE NOWAIT"
	if operation == "update" {
		query = "UPDATE fs_part.heap SET v='A2' WHERE ctid=$1::tid"
	} else if operation == "delete" {
		query = "DELETE FROM fs_part.heap WHERE ctid=$1::tid"
	}
	statement, err := writer.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: writer, Statement: statement, SQL: query}

	freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
	freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE fs_part.unrelated IN ACCESS EXCLUSIVE MODE", 0))
	freezeRun(t, p, b, holder, freezeVoid("SELECT pg_advisory_xact_lock(987654322::bigint) AS repaired"))
	freezeRun(t, p, b, holder, fixture.Select("SELECT k FROM fs_part.heap FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	freezeRun(t, p, b, holder, fixture.Select("SELECT k FROM fs_part.sentinel FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
	oldOwner := freezeAssignedOwner(t, p, b, holder)
	lockedA := a
	lockedA.xmax = oldOwner.xid
	freezeExpect(t, p, b, c, lockedA)
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, &oldOwner, "For Update")
	freezeUnrelatedHeld(t, p, b, c, sentinel, oldOwner)
	freezeBoth(t, p, b, c, freezeError("SELECT k FROM fs_part.heap WHERE ctid=$1::tid FOR UPDATE NOWAIT", "55P03", "could not obtain lock on row in relation \"heap\"", a.tid))

	// Establish a genuine unrelated tuple waiter before surgery. Re-observe
	// the same real wait after repair and later admission; an early grant fails.
	waiterPID := fixture.LockPID(t, p.Ctx, waiter)
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		result, err := waiter.ExecContext(ctx, "UPDATE fs_part.sentinel SET v='B2' WHERE ctid=$1::tid", sentinel.tid)
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	joined := false
	defer func() {
		if !joined {
			cancel()
			outcome := await()
			if outcome.Rows != nil {
				outcome.Rows.Close()
			}
			if outcome.Stmt != nil {
				outcome.Stmt.Close()
			}
		}
	}()
	pending := func() {
		if b.PG {
			fixture.TransactionLockPending(t, p, waiterPID, oldOwner.pid, finished)
		} else {
			fixture.NativeUpdateRowWait(t, p.Ctx, finished)
		}
	}
	pending()
	freezeApply(t, p, b, c, a.tid)
	freezeExpect(t, p, b, c, freezeRepaired(a))
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, nil, "")
	freezeUnrelatedHeld(t, p, b, c, sentinel, oldOwner)
	pending()

	freezeRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
	newOwner := freezeAssignedOwner(t, p, b, writer)
	// A stale RowID-ticket wait must fail promptly, not hang the scenario.
	// This bounds only an actual lock wait; elapsed time never proves admission.
	freezeRun(t, p, b, writer, fixture.Exec("SET LOCAL lock_timeout='10ms'", 0))
	mode := "For Update"
	var successor freezeHeader
	if operation == "nowait" {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, a.tid))
	} else {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, a.tid))
		mode = "Update"
		if operation == "update" {
			mode = "No Key Update"
			successor = freezeOne(t, p, writer, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=1")
			if successor.k != 1 || successor.v != "A2" || successor.tid == a.tid || successor.xmin != newOwner.xid || successor.xmax != "0" {
				t.Fatalf("later UPDATE successor %+v, want new physical A2 version owned by %s with xmax0", successor, newOwner.xid)
			}
		} else {
			freezeRun(t, p, b, writer, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, nil))
		}
	}
	currentA := freezeRepaired(a)
	currentA.xmax = newOwner.xid
	freezeExpect(t, p, b, c, currentA)
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, &newOwner, mode)
	freezeUnrelatedHeld(t, p, b, c, sentinel, oldOwner)
	pending()
	freezeRun(t, p, b, writer, fixture.Exec("COMMIT", 0))
	if operation == "update" {
		freezeExpect(t, p, b, c, successor)
	} else if operation == "delete" {
		freezeExpect(t, p, b, c)
	}
	// Ending the new owner must not reveal the cleared old header again.
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, nil, "")
	freezeUnrelatedHeld(t, p, b, c, sentinel, oldOwner)
	pending()

	freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
	outcome := await()
	joined = true
	if outcome.Err != nil {
		t.Fatal("unrelated waiter after original owner ends", outcome.Err)
	}
	fixture.LockAffected(t, outcome.Result, 1)
	freezeRun(t, p, b, c, fixture.Select("SELECT pg_try_advisory_xact_lock(987654322::bigint) AS acquired", []string{"acquired"}, []string{"BOOL"}, [][]any{{true}}))
	freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
	freezeRun(t, p, b, c, fixture.Exec("LOCK TABLE fs_part.unrelated IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
	freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
	freezeBoth(t, p, b, c, fixture.Select("SELECT k FROM fs_part.sentinel WHERE k=2 FOR UPDATE NOWAIT", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
	values := [][]any{{int64(1), "A"}}
	if operation == "update" {
		values[0][1] = "A2"
	} else if operation == "delete" {
		values = nil
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, values))
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM fs_part.sentinel ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "B2"}}))
	freezeBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.pgrowlocks('fs_part.heap')", []string{"locked_row", "locker", "multi", "xids", "modes", "pids"}, []string{"TID", "XID", "BOOL", "_XID", "_TEXT", "_INT4"}, nil))
	freezeBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.pgrowlocks('fs_part.sentinel')", []string{"locked_row", "locker", "multi", "xids", "modes", "pids"}, []string{"TID", "XID", "BOOL", "_XID", "_TEXT", "_INT4"}, nil))
}

func freezePostrepairInheritedLocks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION IF EXISTS pgrowlocks CASCADE", 0))
	freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pgrowlocks SCHEMA fs_part", 0))
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS pgrowlocks CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
	sentinel := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'B') RETURNING k,v,ctid,xmin,xmax")
	original, keyholder, updater := freezeConn(t, p, b), freezeConn(t, p, b), freezeConn(t, p, b)
	defer original.Close()
	defer keyholder.Close()
	defer updater.Close()
	defer original.ExecContext(p.Ctx, "ROLLBACK")
	defer keyholder.ExecContext(p.Ctx, "ROLLBACK")
	defer updater.ExecContext(p.Ctx, "ROLLBACK")

	const probeSQL = "SELECT k FROM fs_part.heap WHERE ctid=$1::tid FOR UPDATE NOWAIT"
	probeStatement, err := c.PrepareContext(p.Ctx, probeSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer probeStatement.Close()
	probe := fixture.HeldStatement{Executor: c, Statement: probeStatement, SQL: probeSQL}
	const updateSQL = "UPDATE fs_part.heap SET v=$2::text WHERE ctid=$1::tid"
	updateStatement, err := updater.PrepareContext(p.Ctx, updateSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer updateStatement.Close()
	update := fixture.HeldStatement{Executor: updater, Statement: updateStatement, SQL: updateSQL}

	freezeRun(t, p, b, original, fixture.Exec("BEGIN", 0))
	freezeRun(t, p, b, original, fixture.Select("SELECT k FROM fs_part.heap ORDER BY k FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	oldOwner := freezeAssignedOwner(t, p, b, original)
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, &oldOwner, "For Update")
	lockedSentinel := sentinel
	lockedSentinel.xmax = oldOwner.xid
	freezeApply(t, p, b, c, a.tid)
	freezeExpect(t, p, b, c, freezeRepaired(a), lockedSentinel)
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, nil, "")

	freezeRun(t, p, b, keyholder, fixture.Exec("BEGIN", 0))
	freezeRun(t, p, b, keyholder, fixture.Prepared(fixture.Select("SELECT k FROM fs_part.heap WHERE ctid=$1::tid FOR KEY SHARE NOWAIT", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, a.tid)))
	keyOwner := freezeAssignedOwner(t, p, b, keyholder)
	freezeTupleProjection(t, p, b, c, "fs_part.heap", a.tid, &keyOwner, "For Key Share")

	// A normal successor has its own insertion XID, not the repaired xmin2.
	// Keep the original KEY SHARE owner running across two compatible UPDATEs:
	// both genuine successors must inherit that ownership and arbitration.
	current := a
	for _, value := range []string{"A2", "A3"} {
		freezeRun(t, p, b, updater, fixture.Exec("BEGIN", 0))
		updateOwner := freezeAssignedOwner(t, p, b, updater)
		freezeRun(t, p, b, updater, fixture.Exec("SET LOCAL lock_timeout='10ms'", 0))
		fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, current.tid, value))
		successor := freezeOne(t, p, updater, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=1")
		if successor.k != 1 || successor.v != value || successor.tid == current.tid || successor.xmin != updateOwner.xid || successor.xmax != keyOwner.xid {
			t.Fatalf("inherited successor %+v, want new %s version xmin%s/xmax%s", successor, value, updateOwner.xid, keyOwner.xid)
		}
		freezeTupleProjection(t, p, b, updater, "fs_part.heap", successor.tid, &keyOwner, "For Key Share")
		freezeRun(t, p, b, updater, fixture.Exec("COMMIT", 0))
		freezeExpect(t, p, b, c, successor, lockedSentinel)
		freezeTupleProjection(t, p, b, c, "fs_part.heap", successor.tid, &keyOwner, "For Key Share")
		fixture.RunStep(t, p.Ctx, probe, b.Recorder, freezeError(probeSQL, "55P03", "could not obtain lock on row in relation \"heap\"", successor.tid))
		freezeTupleProjection(t, p, b, c, "fs_part.heap", sentinel.tid, &oldOwner, "For Update")
		current = successor
	}

	// Ending only the real inherited owner releases successor arbitration.
	// The original cleared owner still holds its genuine target/sentinel tickets;
	// falling back to that owner's old RowID ticket would reject this NOWAIT.
	freezeRun(t, p, b, keyholder, fixture.Exec("ROLLBACK", 0))
	freezeTupleProjection(t, p, b, c, "fs_part.heap", current.tid, nil, "")
	fixture.RunStep(t, p.Ctx, probe, b.Recorder, fixture.Select(probeSQL, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, current.tid))
	freezeBoth(t, p, b, c, freezeError(probeSQL, "55P03", "could not obtain lock on row in relation \"heap\"", sentinel.tid))
	freezeTupleProjection(t, p, b, c, "fs_part.heap", sentinel.tid, &oldOwner, "For Update")
	freezeRun(t, p, b, original, fixture.Exec("ROLLBACK", 0))
	freezeBoth(t, p, b, c, fixture.Select(probeSQL, []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}, sentinel.tid))
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A3"}, {int64(2), "B"}}))
	freezeBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.pgrowlocks('fs_part.heap')", []string{"locked_row", "locker", "multi", "xids", "modes", "pids"}, []string{"TID", "XID", "BOOL", "_XID", "_TEXT", "_INT4"}, nil))
}
