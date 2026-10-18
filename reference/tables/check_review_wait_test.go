package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// checkReviewSessionID is the session identity pg_blocking_pids reports: the
// backend PID on PostgreSQL, the temporary-namespace number on native.
func checkReviewSessionID(t *testing.T, ctx context.Context, b fixture.Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return fixture.LockPID(t, ctx, c)
	}
	if _, err := c.ExecContext(ctx, "CREATE TEMP SEQUENCE check_review_session_identity"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := c.QueryRowContext(ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	suffix, ok := strings.CutPrefix(name, "pg_temp_")
	id, err := strconv.ParseUint(suffix, 10, 31)
	if !ok || err != nil || id == 0 {
		t.Fatal("native session identity", name, err)
	}
	return int(id)
}

// checkReviewWaitBehind observes, on the same backend, that waiter is blocked
// by holder. A command that completes first fails the test.
func checkReviewWaitBehind(t *testing.T, ctx context.Context, observer *sql.Conn, waiter, holder int, done <-chan fixture.RelationOutcome) {
	t.Helper()
	for {
		select {
		case o := <-done:
			t.Fatalf("command completed before waiting: result=%v error=%v", o.Result, o.Err)
		default:
		}
		var waiting bool
		if err := observer.QueryRowContext(ctx, "SELECT $2::int4 = ANY(pg_blocking_pids($1::int4))", waiter, holder).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}
