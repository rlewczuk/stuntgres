package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Obtain the physical owner through its genuine assigned transaction ID. The
// temporary transaction ends before the worker's implicit statement begins.
func jsonbConcatDeleteGatePID(t *testing.T, ctx context.Context, c *sql.Conn) int {
	t.Helper()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var xid string
	if err := tx.QueryRowContext(ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.QueryRowContext(ctx, "SELECT pid FROM pg_locks WHERE locktype='transactionid' AND transactionid=$1::xid AND granted AND mode='ExclusiveLock'", xid).Scan(&pid); err != nil || pid <= 0 {
		t.Fatal("active cancellation owner", pid, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return pid
}

// The lock key is the rendered result length, not a constant substitute for
// JSONB work. The held lock makes the public admission observation persistent.
func jsonbConcatDeleteGateCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expression string, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
	workerPID := jsonbConcatDeleteGatePID(t, ctx, c)
	holderPID := jsonbConcatDeleteGatePID(t, ctx, holder)
	var key int64
	if err := observer.QueryRowContext(ctx, "SELECT length(("+expression+")::text)::bigint FROM jbcd_active_input WHERE id=2").Scan(&key); err != nil || key <= 0 || key > 1<<32-1 {
		t.Fatal("JSONB result lock key", key, err)
	}
	tx, err := holder.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locked any
	if err := tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock($1::bigint)", key).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			out := fixture.LockResult(t, done)
			if out.Rows != nil {
				out.Rows.Close()
			}
		}
	}()
	go func() { done <- operation(ctx) }()
	for {
		select {
		case out := <-done:
			joined = true
			if out.Rows != nil {
				out.Rows.Close()
			}
			t.Fatal("JSONB statement completed before public gate", out.Err)
		default:
		}
		var waiting bool
		if err := observer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid::bigint=0 AND objid::bigint=$3 AND objsubid=1 AND mode='ExclusiveLock' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, workerPID, holderPID, key).Scan(&waiting); err != nil {
			t.Fatal("JSONB public gate admission", err)
		}
		if waiting {
			break
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("JSONB public gate deadline", err)
		}
		runtime.Gosched()
	}
	if b.PG {
		var signaled bool
		if err := observer.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&signaled); err != nil || !signaled {
			t.Fatal("JSONB backend cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	out := fixture.LockResult(t, done)
	joined = true
	if out.Rows != nil {
		out.Rows.Close()
		t.Fatal("active canceled JSONB statement published cursor")
	}
	if out.Result != nil {
		t.Fatal("active canceled JSONB statement published result")
	}
	if b.PG {
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else {
		fixture.LexicalOrdinary(t, out.Err, context.Canceled)
	}
	// The worker is joined; observe exact-key cleanup through an idle session,
	// not through the native operation's now-canceled context.
	cleanupCtx, cleanupCancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cleanupCancel()
	const gateLocks = `SELECT count(*) FROM pg_locks WHERE pid IN ($1,$2) AND locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid::bigint=0 AND objid::bigint=$3 AND objsubid=1`
	var remaining int64
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, workerPID, key).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("canceled JSONB worker retained gate request or grant", remaining, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, holderPID, key).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("released JSONB gate retained request or grant", remaining, err)
	}
}
