package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func correlationLifecycle(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	q := "SELECT " + correlationProjection("y,x ORDER BY k") + " FROM paired"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, r, correlationStep(q, correlationKnown, pg))
	var retained *sql.Rows
	if !pg {
		retained, err = stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		statisticalMetadata(false, correlationTypes)(t, retained)
	}
	run(fixture.Exec("INSERT INTO paired VALUES (10,9,4)", 1))
	current := []any{float64(1), float64(2.5), float64(10.0 / 3), float64(2.5), float64(6), int64(4), float64(1), float64(1)}
	fixture.RunStep(t, p.Ctx, held, r, correlationStep(q, current, pg))
	run(fixture.Exec("CREATE VIEW paired_view AS "+q, 0))
	viewQ := "SELECT " + joinCorrelationNames() + " FROM paired_view"
	// View columns retain relation metadata, not the computed expression's
	// nonnullable proof. Names, database types and raw values remain exact.
	viewStep := fixture.Select(viewQ, correlationNames, correlationTypes, [][]any{current})
	statisticalBoth(t, p, c, r, viewStep)
	run(fixture.Exec("CREATE TABLE paired_result(corr DOUBLE PRECISION,covar_pop DOUBLE PRECISION,covar_samp DOUBLE PRECISION,regr_avgx DOUBLE PRECISION,regr_avgy DOUBLE PRECISION,regr_count BIGINT,regr_intercept DOUBLE PRECISION,regr_r2 DOUBLE PRECISION)", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec("INSERT INTO paired_result "+q, 1)
		s.Prepared = prepared
		run(s)
	}
	// Stored columns are nullable, unlike the computed pair-count expression;
	// intentionally do not apply aggregate-only Nullable checks to this table.
	run(fixture.Select("SELECT "+joinCorrelationNames()+" FROM paired_result", correlationNames, correlationTypes, [][]any{current, current}))
	if !pg {
		dump := fixture.CheckDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.CheckRoundtrip(t, p, dump)
		statisticalBoth(t, p, c, r, viewStep)
		statisticalMetadata(false, correlationTypes)(t, retained)
		fixture.StringCoreRetainedRows(t, retained, nil, correlationNames, correlationTypes, [][]any{correlationKnown}, false)
	}
	// A later overflowing group must not publish an earlier successful group's
	// count/finalizers to the INSERT target. The inherited runner checks nil
	// failed outputs, exact diagnostics and direct/prepared execution staging.
	run(fixture.Exec("CREATE TABLE paired_bad(g INTEGER,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
	run(fixture.Exec("INSERT INTO paired_bad VALUES (1,3,1),(1,5,2),(2,1e200,1),(2,0,2)", 4))
	bad := "SELECT " + correlationProjection("y,x") + " FROM paired_bad GROUP BY g ORDER BY g"
	statisticalExecutionError(t, p, c, r, bad, "value out of range: overflow", true)
	statisticalExecutionError(t, p, c, r, "INSERT INTO paired_result "+bad, "value out of range: overflow", false)
	run(fixture.Select("SELECT "+joinCorrelationNames()+" FROM paired_result", correlationNames, correlationTypes, [][]any{current, current}))
}

func joinCorrelationNames() string {
	return "corr,covar_pop,covar_samp,regr_avgx,regr_avgy,regr_count,regr_intercept,regr_r2"
}

func TestReferenceCorrelationRegressionLifecycle(t *testing.T) {
	correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		correlationAcceptance(t, p, c, r, pg)
		correlationLifecycle(t, p, c, r, pg)
	})
}

func TestReferenceCorrelationRegressionFeatureSmoke(t *testing.T) {
	correlationScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		correlationAcceptance(t, p, c, r, pg)
		correlationLifecycle(t, p, c, r, pg)
	})
}
