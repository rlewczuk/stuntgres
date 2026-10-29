package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumProgressAdmission(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		holder, worker, observer := vacuumConn(t, p, b), vacuumConn(t, p, b), vacuumConn(t, p, b)
		copyProgressExec(t, p.Ctx, holder, "CREATE TABLE vacuum_progress_admission(id INT)")
		copyProgressExec(t, p.Ctx, holder, "INSERT INTO vacuum_progress_admission VALUES(1),(2)")
		copyProgressExec(t, p.Ctx, holder, "DELETE FROM vacuum_progress_admission WHERE id=1")
		id := copyProgressIdentify(t, p, worker, "vacuum_progress_admission")
		copyProgressExec(t, p.Ctx, holder, "BEGIN")
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		copyProgressExec(t, p.Ctx, holder, "LOCK TABLE vacuum_progress_admission IN SHARE MODE")
		holderPID := 0
		if b.PG {
			holderPID = fixture.LockPID(t, p.Ctx, holder)
		}
		fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, "VACUUM vacuum_progress_admission", "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, int(id.pid), holderPID, id.relid, "ShareUpdateExclusiveLock")
			}
			vacuumProgressEmpty(t, p, b, observer)
		}, func(ctx context.Context) fixture.RelationOutcome {
			var out fixture.RelationOutcome
			out.Result, out.Err = worker.ExecContext(ctx, "VACUUM vacuum_progress_admission")
			return out
		})
		vacuumProgressEmpty(t, p, b, observer)
		vacuumCount(t, p.Ctx, observer, "vacuum_progress_admission", 0)
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		for _, q := range []string{"VACUUM (PROCESS_MAIN false) vacuum_progress_admission", "VACUUM vacuum_progress_admission"} {
			copyProgressExec(t, p.Ctx, worker, q)
			vacuumProgressEmpty(t, p, b, observer)
		}
	})
}
