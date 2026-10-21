package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestampSeriesLifecycle(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const huge = "SELECT generate_series(TIMESTAMP '2000-01-01',TIMESTAMP '2100-01-01',INTERVAL '1 microsecond') AS v"
		const small = "SELECT generate_series($1,TIMESTAMP '2020-03-01',INTERVAL '1 day') AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, small)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: small}
		for _, prepared := range []bool{false, true} {
			if b.PG {
				seriesServerCancel(t, p, b, c, huge, prepared)
			} else {
				for _, query := range []bool{false, true} {
					seriesNativeCancel(t, p, c, huge, prepared, query, "TimestampSeriesCursor).Next")
				}
			}
			fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(small, []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00"), "2020-02-28"))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamp_series_cancelled(v timestamp)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO timestamp_series_cancelled VALUES(TIMESTAMP '1999-12-31')", 1))
		for _, query := range []string{
			"INSERT INTO timestamp_series_cancelled " + huge,
			"CREATE TABLE timestamp_series_cancelled_ctas AS " + huge,
		} {
			for _, prepared := range []bool{false, true} {
				if b.PG {
					seriesServerCancel(t, p, b, c, query, prepared)
				} else {
					seriesNativeCancel(t, p, c, query, prepared, false, "TimestampSeriesCursor).Next")
				}
				fixture.TemporalRunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM timestamp_series_cancelled", []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("1999-12-31 00:00:00")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamp_series_cancelled_ctas(v timestamp)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE timestamp_series_cancelled_ctas", 0))
			}
		}
	})
}

func TestReferenceTimestampSeriesFeatureSmoke(t *testing.T) {
	// Each invoked definition owns its fresh fixture and real public consumers;
	// this smoke retains the same assertions rather than a parallel probe body.
	t.Run("values", TestReferenceTimestampSeriesValues)
	t.Run("held", TestReferenceTimestampSeriesBinding)
	t.Run("metadata", TestReferenceTimestampSeriesMetadata)
	t.Run("overflow", TestReferenceTimestampSeriesDiagnostics)
	t.Run("composition", TestReferenceTimestampSeriesComposition)
	t.Run("atomicity", TestReferenceTimestampSeriesAtomicity)
	t.Run("controls", TestReferenceTimestampSeriesControls)
}
