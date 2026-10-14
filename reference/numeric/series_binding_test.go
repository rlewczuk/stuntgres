package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func seriesDiagnostic(q, token, code, message, hint string) fixture.Diagnostic {
	pos := ""
	if token != "" {
		i := strings.LastIndex(q, token)
		if i < 0 {
			panic("missing diagnostic token")
		}
		pos = fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
	}
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: pos}
}

func TestReferenceSeriesBinding(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN series_int AS integer", 0))
		for _, tc := range []struct {
			args, typ string
			rows      [][]any
		}{
			{"1::smallint,3", "INT4", seriesRows(1, 2, 3)},
			{"1::bigint,3", "INT8", seriesRows(1, 2, 3)},
			{"'1',3", "INT4", seriesRows(1, 2, 3)},
			{"1,'3'", "INT4", seriesRows(1, 2, 3)},
			{"1::series_int,3::series_int", "INT4", seriesRows(1, 2, 3)},
			{"1::bigint,'3'", "INT8", seriesRows(1, 2, 3)},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := fixture.Select("SELECT generate_series("+tc.args+") AS v ORDER BY v", []string{"v"}, []string{tc.typ}, tc.rows)
				seriesBoth(t, p, b, c, s)
			})
		}
		for _, q := range []string{"SELECT pg_catalog.generate_series(1,3) AS v ORDER BY v", `SELECT "generate_series"(1,3) AS v ORDER BY v`, "SELECT generate_series(ALL 1,3) AS v ORDER BY v"} {
			t.Run(q, func(t *testing.T) {
				seriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"INT4"}, seriesRows(1, 2, 3)))
			})
		}
	})
}

func TestReferenceSeriesDiagnostics(t *testing.T) {
	const noMatch = "No function matches the given name and argument types. You might need to add explicit type casts."
	const ambiguous = "Could not choose a best candidate function. You might need to add explicit type casts."
	// Expectations independently specified from released signatures/parser
	// rules and resolved against the pinned PostgreSQL phase-A observations.
	cases := []struct{ call, code, message, hint string }{
		{"generate_series('1','3')", "42725", "function generate_series(unknown, unknown) is not unique", ambiguous},
		{"generate_series(NULL,NULL)", "42725", "function generate_series(unknown, unknown) is not unique", ambiguous},
		{"generate_series($1,$2)", "42725", "function generate_series(unknown, unknown) is not unique", ambiguous},
		{"generate_series(1::smallint,3::smallint)", "42725", "function generate_series(smallint, smallint) is not unique", ambiguous},
		{"generate_series(true,false)", "42883", "function generate_series(boolean, boolean) does not exist", noMatch},
		{"generate_series('1'::text,'3'::text)", "42883", "function generate_series(text, text) does not exist", noMatch},
		{"generate_series(1)", "42883", "function generate_series(integer) does not exist", noMatch},
		{"generate_series(1,2,3,4)", "42883", "function generate_series(integer, integer, integer, integer) does not exist", noMatch},
		{"generate_series(start => 1, stop => 3)", "42883", "function generate_series(start => integer, stop => integer) does not exist", noMatch},
		{"generate_series(VARIADIC ARRAY[1,3])", "42883", "function generate_series(integer[]) does not exist", noMatch},
		{`"GENERATE_SERIES"(1,3)`, "42883", "function GENERATE_SERIES(integer, integer) does not exist", noMatch},
		{"public.generate_series(1,3)", "42883", "function public.generate_series(integer, integer) does not exist", noMatch},
		{"generate_series(DISTINCT 1,3)", "42809", "DISTINCT specified, but generate_series is not an aggregate function", ""},
		{"generate_series(1,3 ORDER BY 1)", "42809", "ORDER BY specified, but generate_series is not an aggregate function", ""},
		{"generate_series(1,3) FILTER (WHERE true)", "42809", "FILTER specified, but generate_series is not an aggregate function", ""},
		{"generate_series(1,3) OVER ()", "42809", "OVER specified, but generate_series is not a window function nor an aggregate function", ""},
		{"generate_series(*)", "42883", "function generate_series() does not exist", noMatch},
		{"generate_series(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", ""},
	}
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.call, func(t *testing.T) {
				for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
					q := prefix + tc.call
					for _, prepared := range []bool{false, true} {
						t.Run(prefix+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, tc.call, tc.code, tc.message, tc.hint))
							if tc.call == "generate_series($1,$2)" {
								s.Args = []any{int64(1), int64(3)}
							}
							s.Prepared, s.SelectRows = prepared, true
							s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				}
			})
		}
		for _, q := range []string{"SELECT generate_series(1,3,0)", "/*é*/ SELECT generate_series(1::bigint,3::bigint,0::bigint)"} {
			t.Run(q, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""))
				s.SelectRows, s.ErrorStage = true, "execute"
				seriesBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceSeriesHeldParameters(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"integer", "bigint"} {
			t.Run(typ, func(t *testing.T) {
				q := "SELECT generate_series($1,3::" + typ + ",$2) AS v ORDER BY v"
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, tc := range []struct {
					start any
					step  int64
					rows  [][]any
				}{
					{int64(1), 1, seriesRows(1, 2, 3)}, {int64(3), 1, seriesRows(3)}, {int64(4), 1, nil}, {nil, 1, nil}, {int64(1), 0, nil}, {int64(1), 1, seriesRows(1, 2, 3)},
				} {
					var s fixture.Step
					if tc.step == 0 {
						s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""), tc.start, tc.step)
						s.SelectRows, s.ErrorStage = true, "execute"
					} else {
						s = fixture.Select(q, []string{"v"}, []string{map[string]string{"integer": "INT4", "bigint": "INT8"}[typ]}, tc.rows, tc.start, tc.step)
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		}
		for _, tc := range []struct{ q, code, message, token, hint string }{
			{"SELECT $1::text,generate_series($1,3)", "42883", "function generate_series(text, integer) does not exist", "generate_series", "No function matches the given name and argument types. You might need to add explicit type casts."},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := seriesDiagnostic(tc.q, tc.token, tc.code, tc.message, tc.hint)
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared, s.SelectRows, s.ErrorStage = true, true, "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		t.Run("repeated_parameter_width", func(t *testing.T) {
			const q = "SELECT generate_series($1,3) AS i,generate_series($1,3::bigint) AS b ORDER BY i"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, s := range []fixture.Step{
				fixture.Select(q, []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}, int64(1)),
				fixture.Select(q, []string{"i", "b"}, []string{"INT4", "INT8"}, nil, int64(4)),
				fixture.Select(q, []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(3)}}, int64(3)),
			} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}
