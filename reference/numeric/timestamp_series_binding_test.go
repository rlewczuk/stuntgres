package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTimestampSeriesBinding(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"SELECT pg_catalog.generate_series(TIMESTAMP '2020-02-28',TIMESTAMP '2020-03-01','1 day') AS v ORDER BY v",
			`SELECT "generate_series"(ALL TIMESTAMP '2020-02-28',TIMESTAMP '2020-03-01',INTERVAL '1 day') AS v ORDER BY v`,
		} {
			timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows("2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00")))
		}
		for _, q := range []string{
			"SELECT generate_series($1::timestamp,$2::timestamp,$3::interval) AS v ORDER BY v",
			"SELECT generate_series($1,TIMESTAMP '2020-03-01',$2) AS v ORDER BY v",
		} {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, tc := range []struct {
					start               any
					step, code, message string
					rows                [][]any
				}{
					{"2020-02-28", "1 day", "", "", timestampSeriesRows("2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00")},
					{"2020-03-02", "1 day", "", "", nil},
					{nil, "0 days", "", "", nil},
					{"2020-02-28", "0 days", "22023", "step size cannot equal zero", nil},
					{"2020-02-28", "infinity", "22023", "step size cannot be infinite", nil},
					{"2020-02-28", "2147483647 months", "22008", "timestamp out of range", nil},
					{"2020-02-28", "1 day", "", "", timestampSeriesRows("2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00")},
				} {
					args := []any{tc.start, tc.step}
					if q == "SELECT generate_series($1::timestamp,$2::timestamp,$3::interval) AS v ORDER BY v" {
						args = []any{tc.start, "2020-03-01", tc.step}
					}
					s := fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, tc.rows, args...)
					if tc.code != "" {
						s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", tc.code, tc.message, ""), args...)
						s.SelectRows, s.ErrorStage = true, "execute"
					}
					fixture.TemporalRunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}()
		}
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ args, signature string }{
			{"TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02'", "timestamp without time zone, timestamp without time zone"},
			{"TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',1", "timestamp without time zone, timestamp without time zone, integer"},
			{"start => TIMESTAMP '2020-01-01',stop => TIMESTAMP '2020-01-02',step => INTERVAL '1 day'", "start => timestamp without time zone, stop => timestamp without time zone, step => interval"},
		} {
			q := "/*é*/ SELECT generate_series(" + tc.args + ")"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "generate_series", "42883", "function generate_series("+tc.signature+") does not exist", hint))
				s.Prepared, s.SelectRows = prepared, true
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferenceTimestampSeriesMetadata(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, args := range []string{
			"TIMESTAMP '2020-01-01 00:00:00.000001',TIMESTAMP '2020-01-01 00:00:00.000001',INTERVAL '1 microsecond'",
			"NULL::timestamp,TIMESTAMP '2020-01-02',INTERVAL '0 days'",
			"TIMESTAMP '2020-01-01',NULL::timestamp,INTERVAL 'infinity'",
			"TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',NULL::interval",
			"TIMESTAMP '2020-01-02',TIMESTAMP '2020-01-01',INTERVAL '1 day'",
		} {
			var expected [][]any
			if args[0] == 'T' && args == "TIMESTAMP '2020-01-01 00:00:00.000001',TIMESTAMP '2020-01-01 00:00:00.000001',INTERVAL '1 microsecond'" {
				expected = timestampSeriesRows("2020-01-01 00:00:00.000001")
			}
			s := fixture.Select("SELECT generate_series("+args+") AS v", []string{"v"}, []string{"TIMESTAMP"}, expected)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.TemporalMetadata(t, b.PG, meta)
				if len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMP" {
					t.Fatal("incorrect typed metadata", meta)
				}
				if nullable, known := meta[0].Nullable(); nullable || known {
					t.Fatalf("expression nullability must be unknown: nullable=%v known=%v", nullable, known)
				}
			}
			timestampSeriesBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceTimestampSeriesDiagnostics(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ start, stop, step, code, message string }{
			{"2020-01-01", "2020-01-02", "0 days", "22023", "step size cannot equal zero"},
			{"2020-01-02", "2020-01-01", "1 month -30 days", "22023", "step size cannot equal zero"},
			{"2020-01-01", "2020-01-02", "1 day -24 hours", "22023", "step size cannot equal zero"},
			{"2020-01-01", "2020-01-02", "infinity", "22023", "step size cannot be infinite"},
			{"2020-01-02", "2020-01-01", "-infinity", "22023", "step size cannot be infinite"},
			{"294276-12-31 23:59:59.999999", "294276-12-31 23:59:59.999999", "1 microsecond", "22008", "timestamp out of range"},
			{"4714-11-24 00:00:00 BC", "4714-11-24 00:00:00 BC", "-1 microsecond", "22008", "timestamp out of range"},
			{"294276-12-31", "294276-12-31", "1 month", "22008", "timestamp out of range"},
			{"294276-12-31", "294276-12-31", "1 day", "22008", "timestamp out of range"},
		} {
			for _, suffix := range []string{"", " LIMIT 1"} {
				q := "/*é*/ SELECT generate_series(TIMESTAMP '" + tc.start + "',TIMESTAMP '" + tc.stop + "',INTERVAL '" + tc.step + "') AS v" + suffix
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", tc.code, tc.message, ""))
				s.SelectRows, s.ErrorStage = true, "execute"
				seriesBoth(t, p, b, c, s)
			}
		}
		q := "/*é*/ SELECT generate_series(NULL::timestamp,TIMESTAMP '2020-01-02','oops'::interval)"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "'oops'", "22007", `invalid input syntax for type interval: "oops"`, ""))
			s.SelectRows, s.Prepared = true, prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q = "SELECT generate_series(TIMESTAMP '294276-12-31 23:59:59.999999',TIMESTAMP '2020-01-01',INTERVAL '1 microsecond') AS v"
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, nil))
	})
}
