package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func statActivityLowXID(t *testing.T, full string) string {
	t.Helper()
	n, err := strconv.ParseUint(full, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(uint32(n))
}

func TestReferencePGStatActivityIdentityAndTracking(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		writer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		activityExec(t, p, o, "CREATE TABLE activity_identity(v integer)")
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		if r := statActivityGet(t, p, o, pid, true); r[17] != nil {
			t.Fatal("read-only BEGIN allocated XID", r)
		}
		activityExec(t, p, peer, "INSERT INTO activity_identity VALUES(7)")
		var full string
		if err := peer.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text").Scan(&full); err != nil {
			t.Fatal(err)
		}
		if r := statActivityGet(t, p, o, pid, true); r.text(17) != statActivityLowXID(t, full) {
			t.Fatal("backend XID not canonical low32", full, r)
		}
		activityExec(t, p, peer, "COMMIT")
		if r := statActivityGet(t, p, o, pid, true); r[17] != nil || r[18] != nil || r[11] != nil {
			t.Fatal("commit retained live identity", r)
		}
		activityExec(t, p, peer, "BEGIN")
		activityExec(t, p, peer, "INSERT INTO activity_identity VALUES(8)")
		fixture.PreparedReferenceCleanup(t, b, "activity_identity_prepared")
		activityExec(t, p, peer, "PREPARE TRANSACTION 'activity_identity_prepared'")
		if r := statActivityGet(t, p, o, pid, true); r[17] != nil || r[18] != nil || r[11] != nil {
			t.Fatal("detached PREPARE appears live", r)
		}
		activityExec(t, p, o, "ROLLBACK PREPARED 'activity_identity_prepared'")
		activityExec(t, p, writer, "BEGIN")
		defer writer.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, writer, "INSERT INTO activity_identity VALUES(9)")
		sleepCtx, sleepCancel := context.WithCancel(p.Ctx)
		defer sleepCancel()
		sleep := fixture.LockOperation(t, sleepCtx, func(ctx context.Context) fixture.RelationOutcome {
			_, err := peer.ExecContext(ctx, "SELECT pg_sleep(600)")
			return fixture.RelationOutcome{Err: err}
		})
		sleepJoined := false
		defer func() {
			sleepCancel()
			if !sleepJoined {
				sleep()
			}
		}()
		active := statActivityAwait(t, p, o, pid, "PgSleep")
		if active[17] != nil || active[18] == nil || active[11] == nil || active[12] == nil || !active.stamp(t, 11).Equal(active.stamp(t, 12)) {
			t.Fatal("active implicit snapshot/transaction authority", active)
		}
		statActivitySignal(t, p, o, pid, false)
		sleepOut := sleep()
		sleepJoined = true
		fixture.AssertDiagnostic(t, sleepOut.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		activityExec(t, p, peer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		activityExec(t, p, peer, "SELECT v FROM activity_identity")
		var horizon string
		if err := peer.QueryRowContext(p.Ctx, "SELECT pg_snapshot_xmin(pg_current_snapshot())::text").Scan(&horizon); err != nil {
			t.Fatal(err)
		}
		retained := statActivityGet(t, p, o, pid, true)
		if retained[17] != nil || retained.text(18) != statActivityLowXID(t, horizon) {
			t.Fatal("RR accepted public horizon", horizon, retained)
		}
		activityExec(t, p, writer, "ROLLBACK")
		if r := statActivityGet(t, p, o, pid, true); r.text(18) != retained.text(18) {
			t.Fatal("peer completion changed retained horizon", r)
		}
		activityExec(t, p, peer, "ROLLBACK")
		activityExec(t, p, peer, "BEGIN")
		activityExec(t, p, peer, "SELECT v FROM activity_identity")
		if r := statActivityGet(t, p, o, pid, true); r[18] != nil {
			t.Fatal("RC transient xmin retained while idle", r)
		}
		activityExec(t, p, peer, "ROLLBACK")
		// Imported accepted horizons outlive the exporter, without acquiring an XID.
		activityExec(t, p, writer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		var snapshot string
		if err := writer.QueryRowContext(p.Ctx, "SELECT pg_export_snapshot()").Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		if err := writer.QueryRowContext(p.Ctx, "SELECT pg_snapshot_xmin(pg_current_snapshot())::text").Scan(&horizon); err != nil {
			t.Fatal(err)
		}
		activityExec(t, p, peer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		activityExec(t, p, peer, "SET TRANSACTION SNAPSHOT '"+snapshot+"'")
		activityExec(t, p, peer, "SELECT v FROM activity_identity")
		activityExec(t, p, writer, "COMMIT")
		if r := statActivityGet(t, p, o, pid, true); r[17] != nil || r.text(18) != statActivityLowXID(t, horizon) {
			t.Fatal("imported horizon lost", r, horizon)
		}
		activityExec(t, p, peer, "ROLLBACK")
		statActivityTracking(t, p, b, o, peer, pid)
		if !b.PG {
			statActivityCluster(t, p, o)
		}
	})
}

func statActivityTracking(t *testing.T, p *fixture.Pair, b fixture.Backend, o, peer *sql.Conn, pid int64) {
	t.Helper()
	setting := "pg_stat_statements.track"
	off, on := "none", "top"
	if b.PG {
		setting = "compute_query_id"
		off, on = "off", "on"
	}
	var original string
	if err := peer.QueryRowContext(p.Ctx, "SELECT current_setting($1)", setting).Scan(&original); err != nil {
		t.Fatal(err)
	}
	defer peer.ExecContext(context.Background(), "SET "+setting+"='"+original+"'")
	activityExec(t, p, peer, "SET "+setting+"='"+off+"'")
	activityExec(t, p, peer, "SELECT v FROM activity_identity WHERE v=7")
	if r := statActivityGet(t, p, o, pid, true); r[19] != nil {
		t.Fatal("uncomputed ID fabricated", r)
	}
	activityExec(t, p, o, "CREATE EXTENSION pg_stat_statements")
	var databaseID, userID string
	if err := peer.QueryRowContext(p.Ctx, "SELECT (SELECT oid::text FROM pg_database WHERE datname=current_database()),(SELECT oid::text FROM pg_roles WHERE rolname=current_user)").Scan(&databaseID, &userID); err != nil {
		t.Fatal("independent computation owner identities", err)
	}
	activityExec(t, p, peer, "SET "+setting+"='"+on+"'")
	// A real per-backend relation makes the source independent of unrelated
	// historical entries; never select a statistics row by the ID under test.
	table := fmt.Sprintf("activity_computed_identity_%d", pid)
	activityExec(t, p, peer, "CREATE TABLE public."+table+"(v integer)")
	defer o.ExecContext(context.Background(), "DROP TABLE public."+table)
	activityExec(t, p, peer, "INSERT INTO public."+table+" VALUES(7)")
	distractor := "SELECT v+1 AS distracting_identity FROM public." + table + " WHERE v=7"
	distractorNormalized := "SELECT v+$1 AS distracting_identity FROM public." + table + " WHERE v=$2"
	activityExec(t, p, peer, distractor)
	var distractorID string
	var distractorRows int
	if err := o.QueryRowContext(p.Ctx, "SELECT min(queryid)::text,count(*) FROM pg_stat_statements WHERE dbid=$1::oid AND userid=$2::oid AND query=$3 AND toplevel", databaseID, userID, distractorNormalized).Scan(&distractorID, &distractorRows); err != nil || distractorRows != 1 || distractorID == "0" {
		t.Fatal("independent existing distractor identity", distractorID, distractorRows, err)
	}
	q := "SELECT /* computed activity identity */ v FROM public." + table + " WHERE v=7"
	normalized := "SELECT /* computed activity identity */ v FROM public." + table + " WHERE v=$1"
	activityExec(t, p, peer, q)
	computed := statActivityGet(t, p, o, pid, true)
	if computed[19] == nil || computed.text(19) == "0" || computed.text(0) != databaseID || computed.text(4) != userID {
		t.Fatal("real analyzed ID absent", computed)
	}
	identityRows, err := o.QueryContext(p.Ctx, "SELECT queryid::text,query,calls,rows FROM pg_stat_statements WHERE dbid=$1::oid AND userid=$2::oid AND query=$3 AND toplevel", databaseID, userID, normalized)
	if err != nil {
		t.Fatal("independent statement shape lookup", err)
	}
	defer identityRows.Close()
	if !identityRows.Next() {
		t.Fatal("missing independent target shape", identityRows.Err())
	}
	var id, representative string
	var calls, rowsCount int64
	if err := identityRows.Scan(&id, &representative, &calls, &rowsCount); err != nil {
		t.Fatal(err)
	}
	if identityRows.Next() || identityRows.Err() != nil {
		t.Fatal("ambiguous target shape", identityRows.Err())
	}
	if err := identityRows.Close(); err != nil {
		t.Fatal(err)
	}
	if representative != normalized || calls != 1 || rowsCount != 1 || id == "0" || id == distractorID || id != computed.text(19) {
		t.Fatal("activity ID does not identify the independently selected target", id, representative, calls, rowsCount, computed)
	}
	activityExec(t, p, peer, "SET "+setting+"='"+off+"'")
	activityExec(t, p, peer, "SELECT 37 /* next uncomputed */")
	if r := statActivityGet(t, p, o, pid, true); r[19] != nil {
		t.Fatal("new uncomputed command retained previous ID", r)
	}
	activityExec(t, p, peer, "SET track_activities=off")
	activityExec(t, p, peer, "SELECT 17 /* disabled */")
	disabled := statActivityGet(t, p, o, pid, true)
	if disabled.text(16) != "disabled" || disabled.text(20) != "" || disabled[10] == nil {
		t.Fatal("disabled activity identity", disabled)
	}
	for _, i := range []int{11, 12, 13, 19} {
		if disabled[i] != nil {
			t.Fatal("disabled field leaked", statActivityNames[i], disabled[i])
		}
	}
	statActivityIdleWait(t, b.PG, disabled) // PGPROC socket waits remain live even with tracking disabled.
	statActivityDisabledWait(t, p, b, o, peer, pid)
	activityExec(t, p, peer, "SET track_activities=on")
	activityExec(t, p, peer, "SELECT 19 /* reenabled */")
	enabled := statActivityGet(t, p, o, pid, true)
	statActivityState(t, b.PG, enabled, "idle", "SELECT 19 /* reenabled */")
	if !enabled.stamp(t, 10).Equal(disabled.stamp(t, 10)) {
		t.Fatal("tracking reminted backend")
	}
	for _, q := range []string{"SELECT 1 /*" + strings.Repeat("x", 1100) + "*/", "SELECT 1 /*" + strings.Repeat("é", 600) + "*/"} {
		activityExec(t, p, peer, q)
		r := statActivityGet(t, p, o, pid, true)
		query := r.text(20)
		end := 1023
		for end > 0 && !utf8.RuneStart(q[end]) {
			end--
		}
		if query != q[:end] || !utf8.ValidString(query) {
			t.Fatalf("query byte clipping len=%d want=%d", len(query), end)
		}
	}
	// Prepared execution retains comments and placeholders rather than arguments.
	q = "SELECT v FROM activity_identity WHERE v=$1 /* original placeholder */"
	stmt, err := peer.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(p.Ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	statActivityState(t, b.PG, statActivityGet(t, p, o, pid, true), "idle", q)
}

func statActivityCluster(t *testing.T, p *fixture.Pair, o *sql.Conn) {
	t.Helper()
	activityExec(t, p, o, "CREATE DATABASE activity_cluster_peer")
	pool := sql.OpenDB(p.NativeConnector.WithDatabase("activity_cluster_peer"))
	defer pool.Close()
	peer, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	pid := statActivityPID(t, p, peer)
	r := statActivityGet(t, p, o, pid, true)
	var oid int64
	if err := o.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname='activity_cluster_peer'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if r.text(0) != fmt.Sprint(oid) || r.text(1) != "activity_cluster_peer" {
		t.Fatal("cluster activity database identity", r)
	}
	separate := stuntgres.NewDriver()
	connector, err := separate.OpenConnector("isolated activity cluster")
	if err != nil {
		t.Fatal(err)
	}
	isolated := sql.OpenDB(connector)
	defer isolated.Close()
	if _, err := isolated.ExecContext(p.Ctx, "SET application_name='isolated activity marker'"); err != nil {
		t.Fatal(err)
	}
	statActivityClear(t, p, o)
	var n int
	if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name='isolated activity marker'").Scan(&n); err != nil || n != 0 {
		t.Fatal("independent cluster leaked", n, err)
	}
}

func statActivityDisabledWait(t *testing.T, p *fixture.Pair, b fixture.Backend, o, peer *sql.Conn, pid int64) {
	t.Helper()
	holder := statActivityConn(t, p, b)
	hpid := statActivityPID(t, p, holder)
	activityExec(t, p, holder, "BEGIN")
	defer holder.ExecContext(context.Background(), "ROLLBACK")
	activityExec(t, p, holder, "LOCK TABLE activity_identity IN ACCESS EXCLUSIVE MODE")
	activityExec(t, p, peer, "BEGIN")
	defer peer.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		_, err := peer.ExecContext(ctx, "LOCK TABLE activity_identity IN ACCESS EXCLUSIVE MODE")
		return fixture.RelationOutcome{Err: err}
	})
	joined := false
	defer func() {
		cancel()
		if !joined {
			await()
		}
	}()
	statActivityBlocker(t, p, o, pid, hpid)
	waiting := statActivityAwait(t, p, o, pid, "relation")
	if waiting.text(14) != "Lock" || waiting.text(16) != "disabled" || waiting.text(20) != "" {
		t.Fatal("disabled real wait ownership", waiting)
	}
	for _, i := range []int{11, 12, 13, 19} {
		if waiting[i] != nil {
			t.Fatal("disabled waiting field leaked", statActivityNames[i], waiting[i])
		}
	}
	var pending int
	if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("disabled pending ownership", pending, err)
	}
	statActivitySignal(t, p, o, pid, false)
	out := await()
	joined = true
	fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	after := statActivityGet(t, p, o, pid, true)
	if after.text(16) != "disabled" || after.text(20) != "" {
		t.Fatal("disabled canceled row lost mask", after)
	}
	for _, i := range []int{11, 12, 13, 19} {
		if after[i] != nil {
			t.Fatal("disabled cancellation leaked field", statActivityNames[i], after[i])
		}
	}
	statActivityIdleWait(t, b.PG, after)
	if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("disabled cancellation retained pending request", pending, err)
	}
	var blockers string
	if err := o.QueryRowContext(p.Ctx, "SELECT pg_blocking_pids($1)::text", pid).Scan(&blockers); err != nil || blockers != "{}" {
		t.Fatal("disabled canceled blocker ownership", blockers, err)
	}
	var grants int
	if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND granted AND locktype='relation' AND relation='activity_identity'::regclass AND mode='AccessExclusiveLock'", hpid).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("disabled cancellation removed holder grant", grants, err)
	}
	if holderRow := statActivityGet(t, p, o, hpid, true); holderRow.text(16) != "idle in transaction" {
		t.Fatal("disabled waiter cancellation ended holder", holderRow)
	}
	activityExec(t, p, peer, "ROLLBACK")
	// Retain the independent successful disabled grant/completion path too.
	activityExec(t, p, peer, "BEGIN")
	joined = false
	await = fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		_, err := peer.ExecContext(ctx, "LOCK TABLE activity_identity IN ACCESS EXCLUSIVE MODE")
		return fixture.RelationOutcome{Err: err}
	})
	statActivityBlocker(t, p, o, pid, hpid)
	grantedWait := statActivityAwait(t, p, o, pid, "relation")
	if grantedWait.text(16) != "disabled" || grantedWait.text(14) != "Lock" {
		t.Fatal("disabled successful wait owner", grantedWait)
	}
	activityExec(t, p, holder, "ROLLBACK")
	out = await()
	joined = true
	if out.Err != nil {
		t.Fatal("disabled successful grant", out.Err)
	}
	statActivityIdleWait(t, b.PG, statActivityGet(t, p, o, pid, true))
	activityExec(t, p, peer, "ROLLBACK")
}
