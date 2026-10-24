package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshPrepared(t *testing.T) {
	txRefreshRoutes(t, []string{"autocommit", "sql", "go"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held, concurrent bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := txRefreshSQL(concurrent)
		// Syntax-only Prepare is legal even before the target exists.
		route := txRefreshHeld(t, p, c, held, q, q+" WITH NO DATA")
		txRefreshSeed(base)
		// DROP INDEX retains its independent transaction policy. Change the
		// committed prerequisite after Prepare, then recreate it privately below.
		if concurrent {
			base(fixture.Exec("DROP INDEX txr_u", 0))
		}
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		attempt := func(s fixture.Step) {
			if mode != "autocommit" {
				run(fixture.Exec("SAVEPOINT eligibility", 0))
			}
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, route(e), b.Recorder, s)
			if mode != "autocommit" {
				run(fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
				run(fixture.Exec("ROLLBACK TO eligibility", 0))
				run(fixture.Exec("RELEASE eligibility", 0))
			}
		}
		if concurrent {
			attempt(fixture.ErrorDiagnostic(q, concurrentNoIndex("txr_mv")))
			run(fixture.Exec("CREATE UNIQUE INDEX txr_u ON txr_mv(i)", 0))
			fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
			attempt(fixture.ErrorDiagnostic(q+" WITH NO DATA", fixture.ControlsReferenceDiagnostic("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA", 0))
			attempt(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")))
			attempt(fixture.ErrorDiagnostic(q+" WITH NO DATA", fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW txr_mv", 0))
		}
		run(fixture.Exec("INSERT INTO txr_source VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
		run(txRefreshRows("txr_mv", 1, 2))
		finish(true)
		// The same held handle must resolve a dropped/recreated target at Execute.
		base(fixture.Exec("DROP MATERIALIZED VIEW txr_mv", 0))
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		attempt(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P01", `relation "txr_mv" does not exist`)))
		finish(false)
		base(fixture.Exec("CREATE MATERIALIZED VIEW txr_mv AS SELECT i FROM txr_source", 2))
		base(fixture.Exec("CREATE UNIQUE INDEX txr_u ON txr_mv(i)", 0))
		fixture.RunStep(t, p.Ctx, route(c), b.Recorder, fixture.Exec(q, 0))
		base(txRefreshRows("txr_mv", 1, 2))
		// Read-only is checked before lookup and concurrency eligibility, including
		// on a genuinely held definition prepared before BEGIN READ ONLY.
		missing := "REFRESH MATERIALIZED VIEW txr_missing"
		if concurrent {
			missing = "REFRESH MATERIALIZED VIEW CONCURRENTLY txr_missing"
		}
		missingRoute := txRefreshHeld(t, p, c, held, missing)
		tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, missingRoute(tx), b.Recorder, fixture.ErrorDiagnostic(missing, fixture.ControlsReferenceDiagnostic("25006", "cannot execute REFRESH MATERIALIZED VIEW in a read-only transaction")))
		fixture.RunStep(t, p.Ctx, route(tx), b.Recorder, fixture.ErrorDiagnostic(q, fixture.TransactionAborted()))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		txRefreshPreparedAuthority(t, p, b, c, mode, held, concurrent)
	})
}

func txRefreshPreparedAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string, held, concurrent bool) {
	base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	base(fixture.Exec("CREATE USER txr_owner", 0))
	base(fixture.Exec("CREATE USER txr_actor", 0))
	fixture.WithPublicSchemaOwner(t, p, b, "txr_actor", func() {
		base(fixture.Exec("ALTER MATERIALIZED VIEW txr_mv OWNER TO txr_owner", 0))
		base(fixture.Exec("GRANT SELECT ON txr_source TO txr_owner", 0))
		base(fixture.Exec("SET SESSION AUTHORIZATION txr_actor", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := txRefreshSQL(concurrent)
		route := txRefreshHeld(t, p, c, held, q)
		// Preparation under an unauthorized role neither looks up authority nor locks.
		base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		base(fixture.Exec("GRANT MAINTAIN ON txr_mv TO txr_actor", 0))
		base(fixture.Exec("SET SESSION AUTHORIZATION txr_actor", 0))
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.Exec(q, 0))
		// A MAINTAIN-only caller has no SELECT authority on producer or target.
		if mode != "autocommit" {
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("SAVEPOINT auth", 0))
		}
		for _, target := range []string{"txr_source", "txr_mv"} {
			kind := "table"
			if target == "txr_mv" {
				kind = "materialized view"
			}
			s := fixture.ErrorDiagnostic("SELECT i FROM "+target, fixture.ControlsReferenceDiagnostic("42501", "permission denied for "+kind+" "+target))
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			if mode != "autocommit" {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("ROLLBACK TO auth", 0))
			}
		}
		finish(false)
		base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		base(fixture.Exec("REVOKE MAINTAIN ON txr_mv FROM txr_actor", 0))
		base(fixture.Exec("SET SESSION AUTHORIZATION txr_actor", 0))
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		fixture.RunStep(t, p.Ctx, route(e), b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for materialized view txr_mv")))
		finish(false)
		base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		base(txRefreshRows("txr_mv", 1, 2))
	})
}
