package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceInsertConflictCanceledProvider(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INT)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		concurrentIndexRows(t, p, b, a, "SELECT n FROM t", 1)
		ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
		w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		if b.pg {
			var canceled bool
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
		} else {
			w.cancel()
		}
		err := w.join(t, p)
		if b.pg {
			want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
			referenceAssertDiagnostic(t, err, &want)
		} else {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, insertConflictNoMatch("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING"))
		if !b.pg {
			concurrentIndexDumpInvalid(t, p, "ui")
		}
		concurrentIndexExec(t, p, b, a, "COMMIT", 0)
		concurrentIndexExec(t, p, b, c, "REINDEX INDEX ui", 0)
		referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING", 0)))
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t", 1)
	})
}
