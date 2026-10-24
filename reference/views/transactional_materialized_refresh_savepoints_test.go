package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshSavepoints(t *testing.T) {
	txRefreshRoutes(t, []string{"sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		txRefreshSeed(base)
		q := txRefreshSQL(concurrent)
		route := txRefreshHeld(t, p, c, held, q, "REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", "REFRESH MATERIALIZED VIEW txr_mv WITH DATA", q+" WITH NO DATA")
		identity := txRefreshIdentity(t, p, c)
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, route(e), b.Recorder, s) }
		run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
		run(fixture.Exec(q, 0))
		run(fixture.Exec("SAVEPOINT kept", 0))
		for _, i := range []int64{3, 4} {
			run(fixture.Exec("INSERT INTO txr_source VALUES($1)", 1, i))
			run(fixture.Exec(q, 0))
			run(txRefreshRows("txr_mv", 1, 2, i))
			run(fixture.Exec("ROLLBACK TO kept", 0))
			run(txRefreshRows("txr_source", 1, 2))
			run(txRefreshRows("txr_mv", 1, 2))
			fixture.MaterializedViewPopulation(run, "txr_mv", true)
			txRefreshSameIdentity(t, p, e, identity)
		}
		run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
		fixture.MaterializedViewPopulation(run, "txr_mv", false)
		run(fixture.MaterializedViewUnpopulated("txr_mv"))
		run(fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
		run(fixture.Exec("ROLLBACK TO kept", 0))
		run(txRefreshRows("txr_mv", 1, 2))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH DATA", 0))
		run(txRefreshRows("txr_mv", 1, 2))
		if concurrent {
			run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
			run(fixture.ErrorDiagnostic(q+" WITH NO DATA", fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")))
			run(fixture.Exec("ROLLBACK TO kept", 0))
			run(txRefreshRows("txr_mv", 1, 2))
		}
		run(fixture.Exec("RELEASE kept", 0))
		finish(false)
		base(txRefreshRows("txr_source", 1))
		base(txRefreshRows("txr_mv", 1))
		txRefreshSameIdentity(t, p, c, identity)
		// Population and rows are also restored by full rollback without a savepoint.
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
		finish(false)
		base(txRefreshRows("txr_mv", 1))
		fixture.MaterializedViewPopulation(base, "txr_mv", true)
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
		finish(true)
		base(fixture.MaterializedViewUnpopulated("txr_mv"))
		base(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH DATA", 0))
		base(txRefreshRows("txr_mv", 1))
		base(fixture.Exec(q, 0))
	})
}
