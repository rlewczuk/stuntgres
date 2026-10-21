package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestampSeriesComposition(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const call = "generate_series(TIMESTAMP '2020-02-28',TIMESTAMP '2020-03-01',INTERVAL '1 day')"
		days := []string{"2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00"}
		for _, q := range []string{
			"SELECT v FROM " + call + " s(v) ORDER BY v",
			"SELECT v FROM ROWS FROM(" + call + ") s(v) ORDER BY v",
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows(days...)))
		}
		rows := timestampSeriesRows(days...)
		for i := range rows {
			rows[i] = append(rows[i], []any{int64(10), int64(11), nil}[i])
		}
		for _, q := range []string{
			"SELECT " + call + " AS v,generate_series(10,11) AS n ORDER BY v",
			"SELECT v,n FROM ROWS FROM(" + call + ",generate_series(10,11)) s(v,n) ORDER BY v",
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v", "n"}, []string{"TIMESTAMP", "INT4"}, rows))
		}
		ord := timestampSeriesRows(days...)
		for i := range ord {
			ord[i] = append(ord[i], int64(i+1))
		}
		timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v,n FROM "+call+" WITH ORDINALITY s(v,n) ORDER BY n", []string{"v", "n"}, []string{"TIMESTAMP", "INT8"}, ord))
		q := "SELECT id,v FROM (VALUES(1),(1),(2)) s(id) CROSS JOIN LATERAL generate_series(TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-01'+id*INTERVAL '1 day',INTERVAL '1 day') g(v) ORDER BY id,v"
		var correlated [][]any
		for _, day := range []string{"2020-01-01 00:00:00", "2020-01-02 00:00:00"} {
			stamp := timestampSeriesRows(day)[0][0]
			correlated = append(correlated, []any{int64(1), stamp}, []any{int64(1), stamp})
		}
		for _, day := range []string{"2020-01-01 00:00:00", "2020-01-02 00:00:00", "2020-01-03 00:00:00"} {
			correlated = append(correlated, []any{int64(2), timestampSeriesRows(day)[0][0]})
		}
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, correlated))
	})
}

func TestReferenceTimestampSeriesAtomicity(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamp_series_atomic(v timestamp)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO timestamp_series_atomic VALUES(TIMESTAMP '1999-12-31')", 1))
		for _, prepared := range []bool{false, true} {
			for _, consumer := range []string{"INSERT INTO timestamp_series_atomic ", "CREATE TABLE timestamp_series_failed AS "} {
				q := consumer + "SELECT generate_series(TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',CASE WHEN id=1 THEN INTERVAL '1 day' ELSE INTERVAL '0 days' END) FROM (VALUES(1),(2)) s(id)"
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""))
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamp_series_atomic ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("1999-12-31 00:00:00")))
			}
			// The failed CTAS must not publish even an empty relation.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamp_series_failed(v timestamp)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE timestamp_series_failed", 0))
		}
		for _, tc := range []struct{ source, code, message string }{
			{"SELECT generate_series(TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',step) FROM (SELECT CASE WHEN id=1 THEN INTERVAL '1 day' ELSE INTERVAL '0 days' END AS step FROM (VALUES(1),(2)) s(id) ORDER BY id) s", "22023", "step size cannot equal zero"},
			{"SELECT generate_series(v,v,INTERVAL '1 microsecond') FROM (SELECT CASE WHEN id=1 THEN TIMESTAMP '2020-01-01' ELSE TIMESTAMP '294276-12-31 23:59:59.999999' END AS v FROM (VALUES(1),(2)) s(id) ORDER BY id) s", "22008", "timestamp out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, prefix := range []string{"INSERT INTO timestamp_series_atomic ", "CREATE TABLE timestamp_series_failed AS "} {
					q := prefix + tc.source
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", tc.code, tc.message, ""))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				var rows *sql.Rows
				var statement *sql.Stmt
				var err error
				if prepared {
					st, e := c.PrepareContext(p.Ctx, tc.source)
					if e != nil {
						t.Fatal(e)
					}
					statement = st
					// Cleanup registration precedes Rows cleanup, so LIFO closes
					// rows first even when an assertion aborts this scenario.
					t.Cleanup(func() { statement.Close() })
					rows, err = statement.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, tc.source)
				}
				if rows != nil {
					t.Cleanup(func() { rows.Close() })
					if !b.PG {
						rows.Close()
						t.Fatal("native published late-failure prefix")
					}
					for rows.Next() {
						var value any
						if e := rows.Scan(&value); e != nil {
							rows.Close()
							t.Fatal(e)
						}
					}
					err = rows.Err()
					rows.Close()
				}
				if statement != nil {
					if closeErr := statement.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				want := seriesDiagnostic(tc.source, "", tc.code, tc.message, "")
				fixture.AssertDiagnostic(t, err, &want)
				timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamp_series_atomic ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("1999-12-31 00:00:00")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamp_series_failed(v timestamp)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE timestamp_series_failed", 0))
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO timestamp_series_atomic SELECT generate_series(TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',INTERVAL '1 day')", 2))
		timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamp_series_atomic ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("1999-12-31 00:00:00", "2020-01-01 00:00:00", "2020-01-02 00:00:00")))
	})
}
