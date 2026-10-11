package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexHashInvalidQuery(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		first, later, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		fp, lp, bp := fixture.ConcurrentIndexPID(t, p, b, first), fixture.ConcurrentIndexPID(t, p, b, later), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, first, "BEGIN", 0)
		fixture.ConcurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(2)", 1)
		build := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)", true)
		fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
		fixture.ConcurrentIndexExec(t, p, b, later, "BEGIN", 0)
		fixture.ConcurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(4)", 1)
		fixture.ConcurrentIndexExec(t, p, b, first, "COMMIT", 0)
		fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
		if b.PG {
			var canceled bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatalf("cancel builder: %v %v", canceled, err)
			}
		} else {
			build.Cancel()
		}
		err := build.Join(t, p)
		if b.PG {
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation: %v", err)
		}
		fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		indexRow := fixture.PgIndexesRow("public", "t", "hi", "CREATE INDEX hi ON public.t USING hash (n)")
		fixture.PgIndexesCheck(t, p, b, c, "hi", indexRow)
		// This successful grandfathered commit intentionally need not maintain
		// the invalid root. Query correctness must therefore come from the heap.
		fixture.ConcurrentIndexExec(t, p, b, later, "COMMIT", 0)
		fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		fixture.PgIndexesCheck(t, p, b, c, "hi", indexRow)
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 4)
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
		fixture.ConcurrentIndexExec(t, p, b, c, "REINDEX INDEX hi", 0)
		fixture.ConcurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 4)
		fixture.PgIndexesCheck(t, p, b, c, "hi", indexRow)
		fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX hi", 0)
		fixture.PgIndexesCheck(t, p, b, c, "hi", nil)
	})
}
