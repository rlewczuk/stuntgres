package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestamptzSeriesComposition(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT v FROM " + timestamptzSeriesNY + " s(v) ORDER BY v", "SELECT v FROM ROWS FROM(" + timestamptzSeriesNY + ") s(v) ORDER BY v"} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesSpring()))
		}
		zip := timestamptzSeriesSpring()
		ord := timestamptzSeriesSpring()
		for i := range zip {
			zip[i] = append(zip[i], []any{int64(10), int64(11), nil}[i])
			ord[i] = append(ord[i], int64(i+1))
		}
		for _, q := range []string{"SELECT " + timestamptzSeriesNY + " AS v,generate_series(10,11) AS n ORDER BY v", "SELECT v,n FROM ROWS FROM(" + timestamptzSeriesNY + ",generate_series(10,11)) s(v,n) ORDER BY v"} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v", "n"}, []string{"TIMESTAMPTZ", "INT4"}, zip))
		}
		timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v,n FROM "+timestamptzSeriesNY+" WITH ORDINALITY s(v,n) ORDER BY n", []string{"v", "n"}, []string{"TIMESTAMPTZ", "INT8"}, ord))
		const empty = "generate_series(NULL::timestamptz,TIMESTAMPTZ '2024-03-11+00',INTERVAL '0 days','No/Such_Zone')"
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT " + empty + " AS v,generate_series(10,11) AS n ORDER BY n", [][]any{{nil, int64(10)}, {nil, int64(11)}}},
			{"SELECT " + empty + " AS v,generate_series(3,1) AS n", nil},
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(tc.q, []string{"v", "n"}, []string{"TIMESTAMPTZ", "INT4"}, tc.rows))
		}
		q := "SELECT id,v FROM (VALUES(1),(1),(2),(3)) s(id) LEFT JOIN LATERAL generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',CASE WHEN id=3 THEN NULL::timestamptz WHEN id=1 THEN TIMESTAMPTZ '2024-03-10 12:00-04' ELSE TIMESTAMPTZ '2024-03-11 12:00-04' END,INTERVAL '1 day','America/New_York') g(v) ON true ORDER BY id,v"
		var rows [][]any
		for _, stamp := range timestamptzSeriesSpring()[:2] {
			rows = append(rows, []any{int64(1), stamp[0]}, []any{int64(1), stamp[0]})
		}
		for _, stamp := range timestamptzSeriesSpring() {
			rows = append(rows, []any{int64(2), stamp[0]})
		}
		rows = append(rows, []any{int64(3), nil})
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TIMESTAMPTZ"}, rows))
	})
}

func TestReferenceTimestamptzSeriesAtomicity(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamptz_series_atomic(v timestamptz)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO timestamptz_series_atomic VALUES(TIMESTAMPTZ '1999-12-31+00')", 1))
		for _, tc := range []struct{ source, code, message string }{
			{"SELECT generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',step,'America/New_York') FROM (SELECT CASE WHEN id=1 THEN INTERVAL '1 day' ELSE INTERVAL '0 days' END AS step FROM (VALUES(1),(2)) s(id) ORDER BY id) s", "22023", "step size cannot equal zero"},
			{"SELECT generate_series(v,v,INTERVAL '1 microsecond','UTC') FROM (SELECT CASE WHEN id=1 THEN TIMESTAMPTZ '2024-01-01+00' ELSE TIMESTAMPTZ '294276-12-31 23:59:59.999999+00' END AS v FROM (VALUES(1),(2)) s(id) ORDER BY id) s", "22008", "timestamp out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, prefix := range []string{"INSERT INTO timestamptz_series_atomic ", "CREATE TABLE timestamptz_series_failed AS "} {
					q := prefix + tc.source
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", tc.code, tc.message, ""))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				// PostgreSQL can stream before a late error; native final publication cannot.
				var rows *sql.Rows
				var err error
				var st *sql.Stmt
				if prepared {
					st, err = c.PrepareContext(p.Ctx, tc.source)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					rows, err = st.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, tc.source)
				}
				if rows != nil {
					defer rows.Close()
					if !b.PG {
						rows.Close()
						t.Fatal("native published late-failure prefix")
					}
					for rows.Next() {
						var v any
						if err := rows.Scan(&v); err != nil {
							t.Fatal(err)
						}
					}
					err = rows.Err()
					rows.Close()
				}
				if st != nil {
					st.Close()
				}
				want := seriesDiagnostic(tc.source, "", tc.code, tc.message, "")
				fixture.AssertDiagnostic(t, err, &want)
				timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamptz_series_atomic ORDER BY v", []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows("1999-12-31 00:00:00")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamptz_series_failed(v timestamptz)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE timestamptz_series_failed", 0))
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO timestamptz_series_atomic SELECT "+timestamptzSeriesNY, 3)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := timestampSeriesRows("1999-12-31 00:00:00")
		for _, row := range timestamptzSeriesSpring() {
			want = append(want, row, row)
		}
		timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamptz_series_atomic ORDER BY v", []string{"v"}, []string{"TIMESTAMPTZ"}, want))
	})
}

func TestReferenceTimestamptzSeriesLifecycle(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const huge = "SELECT generate_series(TIMESTAMPTZ '2000-01-01+00',TIMESTAMPTZ '2100-01-01+00',INTERVAL '1 microsecond','America/New_York') AS v"
		q := "SELECT " + timestamptzSeriesNY + " AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamptz_series_cancelled(v timestamptz)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO timestamptz_series_cancelled VALUES(TIMESTAMPTZ '1999-12-31+00')", 1))
		for _, prepared := range []bool{false, true} {
			if b.PG {
				seriesServerCancel(t, p, b, c, huge, prepared)
			} else {
				for _, query := range []bool{false, true} {
					seriesNativeCancel(t, p, c, huge, prepared, query, "TimestampSeriesCursor).Next")
				}
			}
			fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesSpring()))
			for _, query := range []string{"INSERT INTO timestamptz_series_cancelled " + huge, "CREATE TABLE timestamptz_series_cancelled_ctas AS " + huge} {
				if b.PG {
					seriesServerCancel(t, p, b, c, query, prepared)
				} else {
					seriesNativeCancel(t, p, c, query, prepared, false, "TimestampSeriesCursor).Next")
				}
				timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamptz_series_cancelled", []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows("1999-12-31 00:00:00")))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE timestamptz_series_cancelled_ctas(v timestamptz)", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE timestamptz_series_cancelled_ctas", 0))
			}
		}
		if !b.PG {
			// Fast precancellation is a native API contract, not a stable pq
			// transport observation. PostgreSQL cancellation is admitted above.
			pre, cancelPre := context.WithCancel(p.Ctx)
			cancelPre()
			if rows, err := st.QueryContext(pre); rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal("same-held precancel publication", rows, err)
			}
		}
		fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesSpring()))
		if !b.PG {
			// Native rows own an eager immutable result but iteration retains its
			// originating context, even after the provider itself is exhausted.
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			rows, err := st.QueryContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var saved any
			if err := rows.Scan(&saved); err != nil {
				t.Fatal(err)
			}
			cancel()
			if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
				t.Fatal("retained rows cancellation", rows.Err())
			}
			rows.Close()
			rows.Close()
			if fixture.Normalize(saved) != timestamptzSeriesSpring()[0][0] || len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMPTZ" {
				t.Fatal("saved value/schema changed", saved, meta)
			}
			fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesSpring()))
		}
	})
}

func TestReferenceTimestamptzSeriesFeatureSmoke(t *testing.T) {
	t.Run("values", TestReferenceTimestamptzSeriesValues)
	t.Run("session", TestReferenceTimestamptzSeriesSession)
	t.Run("metadata", TestReferenceTimestamptzSeriesMetadata)
	t.Run("diagnostics", TestReferenceTimestamptzSeriesDiagnostics)
	t.Run("composition", TestReferenceTimestamptzSeriesComposition)
	t.Run("atomicity", TestReferenceTimestamptzSeriesAtomicity)
	t.Run("controls", TestReferenceTimestamptzSeriesControls)
}
