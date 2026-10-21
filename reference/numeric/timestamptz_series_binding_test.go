package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestamptzSeriesBinding(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"SELECT pg_catalog." + timestamptzSeriesNY + " AS v ORDER BY v",
			`SELECT "generate_series"(ALL TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04','1 day','America/New_York') AS v ORDER BY v`,
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesSpring()))
		}
		for _, q := range []string{
			"SELECT generate_series($1::timestamptz,$2::timestamptz,$3::interval,$4::text) AS v ORDER BY v",
			"SELECT generate_series($1,TIMESTAMPTZ '2024-03-11 13:00-04',$2,$3) AS v ORDER BY v",
		} {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, start := range []any{"2024-03-09 12:00-05", []byte("2024-03-09 12:00-05"), time.Date(2024, 3, 9, 17, 0, 0, 0, time.UTC), nil} {
					args := []any{start, "1 day", "America/New_York"}
					if q == "SELECT generate_series($1::timestamptz,$2::timestamptz,$3::interval,$4::text) AS v ORDER BY v" {
						args = []any{start, "2024-03-11 13:00-04", "1 day", "America/New_York"}
					}
					want := timestamptzSeriesSpring()
					if start == nil {
						want = nil
					}
					fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, want, args...))
				}
			}()
		}
		q := "SELECT generate_series($1::timestamptz,$1::timestamptz,INTERVAL '1 day','UTC') AS v"
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows("2024-03-09 17:00:00"), "2024-03-09 17:00+00"))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ args, signature string }{
			{"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00'", "timestamp with time zone, timestamp with time zone"},
			{"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day',1", "timestamp with time zone, timestamp with time zone, interval, integer"},
			{"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day','UTC','UTC'", "timestamp with time zone, timestamp with time zone, interval, unknown, unknown"},
		} {
			q := "/*é*/ SELECT generate_series(" + tc.args + ")"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "generate_series", "42883", "function generate_series("+tc.signature+") does not exist", hint))
				s.SelectRows, s.Prepared = true, prepared
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, owner, hint string }{
			{"/*é*/ SELECT 1 WHERE " + timestamptzSeriesNY + ">TIMESTAMPTZ '2024-01-01+00'", "WHERE", ""},
			{"/*é*/ SELECT CASE WHEN false THEN " + timestamptzSeriesNY + " ELSE TIMESTAMPTZ '2024-01-01+00' END", "CASE", "You might be able to move the set-returning function into a LATERAL FROM item."},
		} {
			seriesBoth(t, p, b, c, fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_series", "0A000", "set-returning functions are not allowed in "+tc.owner, tc.hint)))
		}
		q = "/*é*/ SELECT generate_series($1,$1,$1,'UTC')"
		d := seriesDiagnostic(q, "$1", "42P08", "inconsistent types deduced for parameter $1", "")
		d.Detail = "timestamp with time zone versus interval"
		if !b.PG {
			// The shared native parameter binder retains its existing boundary;
			// timezone-series support does not change generic conflict inference.
			d = seriesDiagnostic(q, "", "0A000", "parameter reused with different destination types", "")
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d, "2024-01-01+00")
			s.SelectRows, s.Prepared = true, prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceTimestamptzSeriesSession(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		const three = "SELECT generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day') AS v ORDER BY v"
		for _, q := range []string{three, "SELECT " + timestamptzSeriesNY + " AS v ORDER BY v"} {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, zone := range []string{"UTC", "America/New_York", "UTC"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
					want := timestamptzSeriesSpring()
					if q == three && zone == "UTC" {
						want = timestamptzSeriesUTC()
					}
					fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, want))
					fixture.TemporalRunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(three, []string{"v"}, []string{"TIMESTAMPTZ"}, timestamptzSeriesUTC()))
				}
			}()
		}
		const q = "SELECT generate_series($1::timestamptz,TIMESTAMPTZ '2024-03-11 13:00-04',$2::interval,$3::text) AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, tc := range []struct {
			start, step, zone any
			rows              [][]any
			message           string
		}{
			{"2024-03-09 12:00-05", "1 day", "America/New_York", timestamptzSeriesSpring(), ""},
			{"2024-03-09 12:00-05", "1 day", "UTC", timestamptzSeriesUTC(), ""},
			{"2024-03-09 12:00-05", "1 day", nil, nil, ""},
			{"2024-03-09 12:00-05", "1 day", "No/Such_Zone", nil, `time zone "No/Such_Zone" not recognized`},
			{nil, "0 days", "No/Such_Zone", nil, ""},
			{"2024-03-09 12:00-05", "0 days", "UTC", nil, "step size cannot equal zero"},
			{"2024-03-09 12:00-05", "infinity", "UTC", nil, "step size cannot be infinite"},
			{"2024-03-09 12:00-05", "1 day", "America/New_York", timestamptzSeriesSpring(), ""},
		} {
			s := fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, tc.rows, tc.start, tc.step, tc.zone)
			if tc.message != "" {
				s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", tc.message, ""), tc.start, tc.step, tc.zone)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Presentation is Execute-local, independently of the explicit stepping zone.
		for _, tc := range []struct{ zone, text string }{{"UTC", "2024-03-09 17:00:00+00"}, {"America/New_York", "2024-03-09 12:00:00-05"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			seriesBoth(t, p, b, c, fixture.Select("SELECT "+timestamptzSeriesNY+"::text AS v LIMIT 1", []string{"v"}, []string{"TEXT"}, [][]any{{tc.text}}))
		}
	})
}

func TestReferenceTimestamptzSeriesMetadata(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, args := range []string{
			"TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-01+00',INTERVAL '1 day','UTC'",
			"NULL::timestamptz,TIMESTAMPTZ '2024-01-01+00',INTERVAL '0 days','No/Such_Zone'",
			"TIMESTAMPTZ '2024-01-02+00',TIMESTAMPTZ '2024-01-01+00',INTERVAL '1 day'",
		} {
			var want [][]any
			if args == "TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-01+00',INTERVAL '1 day','UTC'" {
				want = timestampSeriesRows("2024-01-01 00:00:00")
			}
			s := fixture.Select("SELECT generate_series("+args+")", []string{"generate_series"}, []string{"TIMESTAMPTZ"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(meta) != 1 || meta[0].Name() != "generate_series" || meta[0].DatabaseTypeName() != "TIMESTAMPTZ" || meta[0].ScanType() != reflect.TypeFor[time.Time]() {
					t.Fatal("timestamptz metadata", meta)
				}
				if _, known := meta[0].Length(); known {
					t.Fatal("invented length")
				}
				if _, _, known := meta[0].DecimalSize(); known {
					t.Fatal("invented precision")
				}
				if nullable, known := meta[0].Nullable(); nullable || known {
					t.Fatal("invented nullability")
				}
			}
			timestampSeriesBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceTimestamptzSeriesRoutineMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"timestamptz_series_reader"})
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Use the existing public pg_proc projection on both backends. The
		// provider authority unit independently checks set-returning status,
		// exact signatures, no defaults and nonvariadic declarations.
		q := "SELECT oid::bigint AS oid,prorettype::bigint AS result,provolatile::text AS volatility,proisstrict,proparallel::text AS parallel,pronargs::integer AS nargs FROM pg_proc WHERE oid IN (939,6274) ORDER BY oid"
		seriesBoth(t, p, b, c, fixture.Select(q, []string{"oid", "result", "volatility", "proisstrict", "parallel", "nargs"}, []string{"INT8", "INT8", "TEXT", "BOOL", "TEXT", "INT4"}, [][]any{
			{int64(939), int64(1184), "s", true, "s", int64(3)},
			{int64(6274), int64(1184), "i", true, "s", int64(4)},
		}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE timestamptz_series_reader", 0))
		for _, signature := range []string{"pg_catalog.generate_series(timestamptz,timestamptz,interval)", "pg_catalog.generate_series(timestamptz,timestamptz,interval,text)"} {
			func() {
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
						t.Error(err)
					}
				}()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC,timestamptz_series_reader", 0))
				call := timestamptzSeriesNY
				if signature == "pg_catalog.generate_series(timestamptz,timestamptz,interval)" {
					call = "generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day')"
				}
				query := "SELECT " + call + " AS v ORDER BY v"
				fixture.RoleGrantActor(run, "timestamptz_series_reader", func() { fixture.RoleGrantPermissionDenied(run, query, "function", "generate_series") })
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO timestamptz_series_reader", 0))
				want := timestamptzSeriesSpring()
				if signature == "pg_catalog.generate_series(timestamptz,timestamptz,interval)" {
					want = timestamptzSeriesUTC()
				}
				fixture.RoleGrantActor(run, "timestamptz_series_reader", func() {
					timestampSeriesBoth(t, p, b, c, fixture.Select(query, []string{"v"}, []string{"TIMESTAMPTZ"}, want))
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM timestamptz_series_reader", 0))
				fixture.RoleGrantActor(run, "timestamptz_series_reader", func() { fixture.RoleGrantPermissionDenied(run, query, "function", "generate_series") })
			}()
		}
	})
}
