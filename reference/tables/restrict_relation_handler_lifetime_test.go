package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRestrictRelationKindForeignHandlerLifetime(t *testing.T) {
	for _, inquiry := range []string{"analyze", "capability"} {
		t.Run(inquiry, func(t *testing.T) {
			fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, _ func(fixture.Step)) {
				// Each inquiry owns a fresh scenario. Cached-handler observations
				// do not authorize reuse after unrelated DDL or failed statements.
				run := func(step fixture.Step) {
					t.Helper()
					if !t.Run(step.Query, func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}) {
						t.FailNow()
					}
				}
				fdwLive(t, p, b, r, run)
				// Preserve the exact observed warm-up sequence: no catalog work
				// or restriction failures between warming and the single inquiry.
				run(fixture.Exec("TRUNCATE fdw_data", 0))
				fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES(1,'one',true),(2,NULL,false),(3,'three',NULL)", 3)
				run(fdwRows(rkForeignRead, rkForeignBaseline()))
				rkForeignSet(run, "foreign-table")
				q := "ANALYZE fdw_data"
				want := fixture.Exec(q, 0)
				if inquiry == "capability" {
					q = "SELECT is_updatable FROM information_schema.columns WHERE table_schema='public' AND table_name='fdw_data' AND column_name='id'"
					want = fixture.Select(q, []string{"is_updatable"}, []string{"VARCHAR"}, [][]any{{"YES"}})
				}
				if !b.PG {
					want = rkForeignDenied(q, false)
				}
				run(want)
				fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", rkForeignBaseline()))
				run(fixture.Exec("RESET "+rkForeignSetting, 0))
				run(fdwRows(rkForeignRead, rkForeignBaseline()))
			})
		})
	}
}
