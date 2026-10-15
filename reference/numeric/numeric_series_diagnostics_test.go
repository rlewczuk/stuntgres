package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNumericSeriesDiagnostics(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct{ args, message string }{
			{"1::numeric,2,0.000", "step size cannot equal zero"},
			{"2::numeric,1,-0.000", "step size cannot equal zero"},
			{"'NaN'::numeric,'Infinity'::numeric,0", "start value cannot be NaN"},
			{"1::numeric,'Infinity'::numeric,'NaN'::numeric", "stop value cannot be infinity"},
			{"2::numeric,1,'NaN'::numeric", "step size cannot be NaN"},
		}
		for role, label := range []string{"start value", "stop value", "step size"} {
			for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
				args := []string{"1::numeric", "2::numeric", "1::numeric"}
				args[role] = "'" + special + "'::numeric"
				word := "infinity"
				if special == "NaN" {
					word = "NaN"
				}
				cases = append(cases, struct{ args, message string }{args[0] + "," + args[1] + "," + args[2], label + " cannot be " + word})
			}
		}
		for _, tc := range cases {
			t.Run(tc.args, func(t *testing.T) {
				q := "/*é*/ SELECT generate_series(" + tc.args + ")"
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", tc.message, ""))
				s.SelectRows, s.ErrorStage = true, "execute"
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceNumericSeriesOracleDecisions(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, token, code, message string }{
			{"SELECT generate_series(NULL::numeric,1::numeric/0,1)", "", "22012", "division by zero"},
			{"/*é*/ SELECT generate_series(NULL::numeric,'oops'::numeric,1)", "'oops'", "22P02", `invalid input syntax for type numeric: "oops"`},
			{"SELECT generate_series(1e-16384,1,1)", "1e-16384", "22003", "value overflows numeric format"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, tc.token, tc.code, tc.message, "")))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE numeric_series_nullable(n numeric, bad text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO numeric_series_nullable VALUES(NULL,'oops')", 1))
		for _, q := range []string{
			"SELECT generate_series(n,1,0) AS v FROM numeric_series_nullable",
			"SELECT generate_series(1,n,0) AS v FROM numeric_series_nullable",
			"SELECT generate_series(1,2,n) AS v FROM numeric_series_nullable",
			"SELECT generate_series(n,'NaN'::numeric,'Infinity'::numeric) AS v FROM numeric_series_nullable",
		} {
			seriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, nil))
		}
		q := "SELECT generate_series(n,bad::integer,1) FROM numeric_series_nullable"
		s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22P02", `invalid input syntax for type integer: "oops"`, ""))
		s.SelectRows, s.ErrorStage = true, "execute"
		seriesBoth(t, p, b, c, s)
	})
}

func TestReferenceNumericSeriesBinding(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN numeric_series_domain AS numeric(4,2)", 0))
		for _, tc := range []struct {
			args, typ string
			rows      [][]any
		}{
			{"1,3", "INT4", seriesRows(1, 2, 3)},
			{"1::bigint,3", "INT8", seriesRows(1, 2, 3)},
			{"1.0,3", "NUMERIC", numericSeriesRows("1.0", "2.0", "3.0")},
			{"1::bigint,3::numeric", "NUMERIC", numericSeriesRows("1", "2", "3")},
			{"1::numeric,'3'", "NUMERIC", numericSeriesRows("1", "2", "3")},
			{"1::numeric_series_domain,3::numeric_series_domain", "NUMERIC", numericSeriesRows("1.00", "2.00", "3.00")},
		} {
			t.Run(tc.args, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select("SELECT generate_series("+tc.args+") AS v ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows))
			})
		}
		const noMatch = "No function matches the given name and argument types. You might need to add explicit type casts."
		const ambiguous = "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, tc := range []struct{ args, code, signature, hint string }{
			{"'1'::text,'3'::text", "42883", "text, text", noMatch},
			{"1::real,3::real", "42883", "real, real", noMatch},
			{"1::double precision,3::double precision", "42883", "double precision, double precision", noMatch},
			{"'1','3'", "42725", "unknown, unknown", ambiguous},
			{"NULL,NULL", "42725", "unknown, unknown", ambiguous},
			{"$1,$2", "42725", "unknown, unknown", ambiguous},
			{"start => 1::numeric,stop => 3::numeric", "42883", "start => numeric, stop => numeric", noMatch},
			{"1::numeric", "42883", "numeric", noMatch},
			{"VARIADIC ARRAY[1::numeric,3::numeric]", "42883", "numeric[]", noMatch},
		} {
			t.Run(tc.args, func(t *testing.T) {
				q := "/*é*/ SELECT generate_series(" + tc.args + ")"
				message := "function generate_series(" + tc.signature + ") does not exist"
				if tc.code == "42725" {
					message = "function generate_series(" + tc.signature + ") is not unique"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "generate_series", tc.code, message, tc.hint))
					s.Prepared, s.SelectRows = prepared, true
					s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					if tc.args == "$1,$2" {
						s.Args = []any{"1", "3"}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, tc := range []struct{ q, owner string }{
			{"SELECT 1 WHERE generate_series(0.1,0.3,0.1)>0", "WHERE"},
			{"SELECT 1 LIMIT generate_series(0.1,0.3,0.1)", "LIMIT"},
		} {
			s := fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, "generate_series", "0A000", "set-returning functions are not allowed in "+tc.owner, ""))
			seriesBoth(t, p, b, c, s)
		}
	})
}
