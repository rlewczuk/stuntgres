package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Resolve the physical owner through its assigned XID's genuine lock. End this
// temporary transaction before the held statement's implicit transaction begins.
func binaryConversionGatePID(t *testing.T, ctx context.Context, c *sql.Conn) int {
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
		t.Fatal("binary conversion owner", pid, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return pid
}

// The SQL derives its keys from actual conversion output: 2 for one e9 byte,
// 67108864 for the unchanged 32MiB payload. This witnesses active SQL after
// conversion, not interruption inside a provider. The holder makes admission
// persistent until cancellation and join; the write also retains its first
// validated destination row's grant, not a published row.
func binaryConversionGateCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, requireSmallGrant bool, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
	workerPID := binaryConversionGatePID(t, ctx, c)
	holderPID := binaryConversionGatePID(t, ctx, holder)
	tx, err := holder.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			if err := tx.Rollback(); err != nil {
				t.Error("binary conversion holder rollback", err)
			}
		}
	}()
	var locked any
	if err := tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock(67108864::bigint)").Scan(&locked); err != nil {
		t.Fatal(err)
	}
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	// Registered after holder rollback so even assertion-failure exits cancel
	// and bounded-join the worker before releasing its blocker or caller handles.
	defer func() {
		cancel()
		if !joined {
			out := fixture.LockResult(t, done)
			if out.Rows != nil {
				if err := out.Rows.Close(); err != nil {
					t.Error("binary conversion failure cursor close", err)
				}
			}
		}
	}()
	go func() { done <- operation(ctx) }()
	for {
		select {
		case out := <-done:
			joined = true
			if out.Rows != nil {
				if err := out.Rows.Close(); err != nil {
					t.Error("binary conversion early cursor close", err)
				}
			}
			t.Fatal("binary conversion completed before public gate", out.Err)
		default:
		}
		// All facts are required together: exact worker wait, actual blocker,
		// matching holder grant, and (for ordered INSERT) the earlier small grant.
		var admitted bool
		if err := observer.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=67108864 AND objsubid=1
				AND mode='ExclusiveLock' AND NOT granted)
			AND $2=ANY(pg_blocking_pids($1))
			AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$2 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=67108864 AND objsubid=1
				AND mode='ExclusiveLock' AND granted)
			AND (NOT $3 OR EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=2 AND objsubid=1
				AND mode='ExclusiveLock' AND granted))`, workerPID, holderPID, requireSmallGrant).Scan(&admitted); err != nil {
			t.Fatal("binary conversion public gate admission", err)
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("binary conversion public gate deadline", err)
		}
		if admitted {
			break
		}
		runtime.Gosched()
	}
	if b.PG {
		var signaled bool
		if err := observer.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&signaled); err != nil || !signaled {
			t.Fatal("binary conversion backend cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	out := fixture.LockResult(t, done)
	joined = true
	if out.Rows != nil {
		if err := out.Rows.Close(); err != nil {
			t.Error("binary conversion canceled cursor close", err)
		}
		t.Fatal("active canceled binary conversion published cursor")
	}
	if out.Result != nil {
		t.Fatal("active canceled binary conversion published result")
	}
	if b.PG {
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else {
		fixture.LexicalOrdinary(t, out.Err, context.Canceled)
	}
	// Inspect requests AND grants using a live idle observer, never the canceled
	// worker context. Check both keys even when read order was unspecified.
	cleanupCtx, cleanupCancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cleanupCancel()
	const gateLocks = `SELECT count(*) FROM pg_locks WHERE pid IN ($1,$2)
		AND locktype='advisory'
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid::bigint=0 AND objid::bigint IN (2,67108864) AND objsubid=1`
	var remaining int64
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, workerPID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("canceled binary conversion worker retained gate request or grant", remaining, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("binary conversion holder rollback", err)
	}
	released = true
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, holderPID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("released binary conversion gate retained request or grant", remaining, err)
	}
}
