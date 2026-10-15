package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateCancellation(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_active(v INT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_active SELECT generate_series(1,64)", 64))
		q := "SELECT " + hypotheticalProjection("4,4", "a.v+b.v+c.v DESC,a.v+b.v+c.v ASC", "") + " FROM (SELECT v FROM hs_active ORDER BY v LIMIT $1) AS a CROSS JOIN (SELECT v FROM hs_active ORDER BY v LIMIT $1) AS b CROSS JOIN (SELECT v FROM hs_active ORDER BY v LIMIT $1) AS c"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, prepared := range []bool{false, true} {
			// Observe this worker inside aggregate execution, before child drain.
			// Waiting for its later sort excludes active input materialization
			// from admission and can miss the fixed deadline under race load.
			// Private units separately cancel inside actual sorting/comparison.
			fixture.BinaryConversionActiveCancel(t, p, b, c, q, "openAggregateNode", func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx, int64(64))
				} else {
					rows, err = c.QueryContext(ctx, q, int64(64))
				}
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			orderedSetBoth(t, p, c, b, orderedSetValue("SELECT count(*) AS v FROM hs_active", "INT8", int64(64)))
			s := hypotheticalResult(q, hypotheticalRow(5, 3, .5, 8.0/9))
			s.Args = []any{int64(2)}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		}
	})
}
