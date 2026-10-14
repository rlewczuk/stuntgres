package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSeriesOracleDecisions(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, token, code, message, hint string }{
			{"SELECT generate_series(1,)", ")", "42601", `syntax error at or near ")"`, ""},
			{"SELECT 1 WHERE generate_series(true,false)>0", "generate_series", "42883", "function generate_series(boolean, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT generate_series(NULL::integer,1/0)", "", "22012", "division by zero", ""},
			{"SELECT generate_series(NULL::integer,'bad'::integer)", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, ""},
			{"SELECT generate_series(1,'2147483648'::integer)", "'2147483648'", "22003", `value "2147483648" is out of range for type integer`, ""},
			{"VALUES(generate_series(1,2))", "generate_series", "0A000", "set-returning functions are not allowed in VALUES", ""},
			{"PREPARE series_unknown AS SELECT generate_series($1,$2)", "generate_series", "42725", "function generate_series(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(tc.q, seriesDiagnostic(tc.q, tc.token, tc.code, tc.message, tc.hint))
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE series_nullable(n integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO series_nullable VALUES(NULL)", 1))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT generate_series(1,3)+n AS v FROM series_nullable", [][]any{{nil}, {nil}, {nil}}},
			{"SELECT generate_series(1,3)+NULL::integer AS v,generate_series(10,11) AS w", [][]any{{nil, int64(10)}, {nil, int64(11)}}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				cols, types := []string{"v"}, []string{"INT4"}
				if len(tc.rows[0]) == 2 {
					cols, types = []string{"v", "w"}, []string{"INT4", "INT4"}
				}
				s := fixture.Select(tc.q, cols, types, tc.rows)
				s.Bag = true
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE series_inferred AS SELECT generate_series($1,3) AS v ORDER BY v", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("EXECUTE series_inferred(1)", []string{"v"}, []string{"INT4"}, seriesRows(1, 2, 3)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE series_values(v integer)", 0))
		t.Run("insert_values_owner", func(t *testing.T) {
			q := "INSERT INTO series_values VALUES(generate_series(1,2))"
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM series_values", 0))
					s := fixture.Exec(q, 2)
					if !b.PG {
						s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "generate_series", "0A000", "generate_series is unsupported in INSERT VALUES", ""))
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					rows := seriesRows(1, 2)
					if !b.PG {
						rows = nil
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM series_values ORDER BY v", []string{"v"}, []string{"INT4"}, rows))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM series_values", map[bool]int64{false: 0, true: 2}[b.PG]))
				})
			}
		})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public", 0))
		seriesBoth(t, p, b, c, fixture.Select("SELECT generate_series(1,2) AS v ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2)))
	})
}
