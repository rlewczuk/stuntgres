package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictCanceledProvider(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INT)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t", 1)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		if b.PG {
			var canceled bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
		} else {
			w.Cancel()
		}
		err := w.Join(t, p)
		if b.PG {
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
		} else {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, insertConflictNoMatch("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING"))
		if !b.PG {
			fixture.ConcurrentIndexDumpInvalid(t, p, "ui")
		}
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "REINDEX INDEX ui", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING", 0)))
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t", 1)
	})
}
