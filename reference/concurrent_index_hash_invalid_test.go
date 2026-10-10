package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres"
)

func TestReferenceConcurrentIndexHashInvalidQuery(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		first, later, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		fp, lp, bp := concurrentIndexPID(t, p, b, first), concurrentIndexPID(t, p, b, later), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, first, "BEGIN", 0)
		concurrentIndexExec(t, p, b, first, "INSERT INTO t VALUES(2)", 1)
		build := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY hi ON t USING hash(n)", true)
		concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Live: true}, bp, fp, "waiting for writers before build")
		concurrentIndexExec(t, p, b, later, "BEGIN", 0)
		concurrentIndexExec(t, p, b, later, "INSERT INTO t VALUES(4)", 1)
		concurrentIndexExec(t, p, b, first, "COMMIT", 0)
		concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, bp, lp, "waiting for writers before validation")
		if b.pg {
			var canceled bool
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatalf("cancel builder: %v %v", canceled, err)
			}
		} else {
			build.cancel()
		}
		err := build.join(t, p)
		if b.pg {
			want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
			referenceAssertDiagnostic(t, err, &want)
		} else if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation: %v", err)
		}
		concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		indexRow := pgIndexesRow("public", "t", "hi", "CREATE INDEX hi ON public.t USING hash (n)")
		pgIndexesCheck(t, p, b, c, "hi", indexRow)
		// This successful grandfathered commit intentionally need not maintain
		// the invalid root. Query correctness must therefore come from the heap.
		concurrentIndexExec(t, p, b, later, "COMMIT", 0)
		concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		pgIndexesCheck(t, p, b, c, "hi", indexRow)
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 4)
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
		concurrentIndexExec(t, p, b, c, "REINDEX INDEX hi", 0)
		concurrentIndexWait(t, p, b, "hi", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t WHERE n=4", 4)
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 4)
		pgIndexesCheck(t, p, b, c, "hi", indexRow)
		concurrentIndexExec(t, p, b, c, "DROP INDEX hi", 0)
		pgIndexesCheck(t, p, b, c, "hi", nil)
	})
}
