package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshLifecycle(t *testing.T) {
	txRefreshRoutes(t, []string{"autocommit", "sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		txRefreshSeed(base)
		q := txRefreshSQL(concurrent)
		route := txRefreshHeld(t, p, c, held, q, q+" WITH DATA")
		base(txRefreshRows("txr_mv", 1)) // Prepare did not run the producer.
		identity := txRefreshIdentity(t, p, c)
		for _, commit := range []bool{false, true} {
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
			run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
			s := txRefreshRows("txr_mv", 1, 2)
			s.CheckMetadata = txRefreshMetadata(b.PG)
			run(s)
			run(txRefreshRows("txr_source", 1, 2))
			fixture.MaterializedViewPopulation(run, "txr_mv", true)
			txRefreshSameIdentity(t, p, e, identity)
			// A second real Execute uses current own producer input, not a cached result.
			fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q+" WITH DATA", 0))
			run(txRefreshRows("txr_mv", 1, 2))
			if mode != "autocommit" {
				other(txRefreshRows("txr_source", 1))
				if concurrent {
					other(txRefreshRows("txr_mv", 1))
				}
			}
			finish(commit)
			if commit || mode == "autocommit" {
				other(txRefreshRows("txr_source", 1, 2))
				other(txRefreshRows("txr_mv", 1, 2))
			} else {
				other(txRefreshRows("txr_source", 1))
				other(txRefreshRows("txr_mv", 1))
			}
			base(fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
			txRefreshSameIdentity(t, p, c, identity)
			if mode == "autocommit" && !commit {
				base(fixture.Exec("DELETE FROM txr_source WHERE i=2", 1))
				fixture.RunStep(t, p.Ctx, route(c), b.Recorder, fixture.Exec(q, 0))
			}
		}
	})
}
