package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFullFeatureSmoke(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		f.run(fixture.Exec("CREATE TABLE smoke_full(id INT PRIMARY KEY,value TEXT)", 0))
		f.run(fixture.Exec("INSERT INTO smoke_full VALUES(1,'one'),(2,NULL),(3,'three')", 3))
		f.run(fixture.Exec("DELETE FROM smoke_full WHERE id=2", 1))
		before, ix := f.locator("smoke_full"), f.locator("smoke_full_pkey")
		f.command(fixture.Exec("VACUUM (FULL,INDEX_CLEANUP OFF,PARALLEL 0) smoke_full", 0))
		before = f.rewritten("smoke_full", before)
		f.rewritten("smoke_full_pkey", ix)
		want := fixture.Select("SELECT id,value FROM smoke_full ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(3), "three"}})
		f.run(want)
		f.run(fixture.Select("SELECT value FROM smoke_full WHERE id=3", []string{"value"}, []string{"TEXT"}, [][]any{{"three"}}))
		duplicate := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "smoke_full_pkey"`)
		duplicate.Detail = "Key (id)=(3) already exists."
		duplicate.Schema, duplicate.Table, duplicate.Constraint = "public", "smoke_full", "smoke_full_pkey"
		f.run(fixture.ErrorDiagnostic("INSERT INTO smoke_full VALUES(3,'bad')", duplicate))
		f.command(fixture.Exec("VACUUM FULL smoke_full", 0))
		before = f.rewritten("smoke_full", before)
		f.run(fixture.Exec("CREATE TABLE smoke_full_empty(id INT)", 0))
		empty := f.locator("smoke_full_empty")
		f.command(fixture.Exec("VACUUM FULL smoke_full_empty", 0))
		f.rewritten("smoke_full_empty", empty)
		f.command(fixture.Exec("VACUUM (FULL,ANALYZE) smoke_full(value)", 0))
		before = f.rewritten("smoke_full", before)
		vacuumStatistic(f.t, f.p.Ctx, f.c, "smoke_full", "analyze_count", 1)
		vacuumCount(f.t, f.p.Ctx, f.c, "smoke_full", 0)
		f.command(fixture.ErrorDiagnostic("VACUUM (FULL,PARALLEL 1) smoke_full", fixture.ControlsReferenceDiagnostic("0A000", "VACUUM FULL cannot be performed in parallel")))
		f.unchanged("smoke_full", before)
		holder := vacuumConn(f.t, f.p, f.b)
		vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("BEGIN", 0))
		defer holder.ExecContext(f.p.Ctx, "ROLLBACK")
		vacuumRows(f.t, f.p, f.b, holder, "smoke_full", 1, 3)
		var notices []fixture.Diagnostic
		_, skipper, rec := fixture.NoticeReferencePool(f.t, f.p, f.b.PG, &notices)
		skip := fixture.Exec("VACUUM (FULL,SKIP_LOCKED) smoke_full", 0)
		if f.prepared {
			fixture.RunStep(f.t, f.p.Ctx, vacuumHeld(f.t, f.p, skipper, skip.Query), rec, skip)
		} else {
			fixture.RunStep(f.t, f.p.Ctx, skipper, rec, skip)
		}
		vacuumWarning(f.t, notices, "55P03", `skipping vacuum of "smoke_full" --- lock not available`)
		f.unchanged("smoke_full", before)
		pid, blocker := fixture.LockPID(f.t, f.p.Ctx, f.c), fixture.LockPID(f.t, f.p.Ctx, holder)
		cancel, join, finished := f.start("VACUUM FULL smoke_full")
		fixture.RelationLockPending(f.t, f.p, f.b, pid, blocker, before.oid, "AccessExclusiveLock", finished)
		observer := vacuumConn(f.t, f.p, f.b)
		fullCancel(f, observer, pid, cancel, join, "")
		f.unchanged("smoke_full", before)
		vacuumRun(f.t, f.p, f.b, holder, fixture.Exec("ROLLBACK", 0))
		f.command(fixture.Exec("VACUUM FULL smoke_full", 0))
		f.rewritten("smoke_full", before)
		f.run(want)
		f.emptyProgress()
	})
}
