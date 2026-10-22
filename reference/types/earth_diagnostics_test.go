package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthDiagnostics(t *testing.T) {
	cases := []struct{ input, constraint string }{
		{"(6378168,0,0,0)", "not_3d"},
		{"(6378168,0,0,0),(6378169,0,0,0)", "not_3d"},
		{"(0,0,0),(1,1,1)", "not_point"},
		{"(6378168,NaN),(6378168,NaN)", "not_point"},
		{"()", "on_surface"}, {"(0)", "on_surface"}, {"(NaN)", "on_surface"},
		{"(Infinity,0,0)", "on_surface"}, {"(-Infinity)", "on_surface"},
		{"(1e308)", "on_surface"}, {"6378161", "on_surface"}, {"6378175", "on_surface"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE earth_diag(id integer NOT NULL,v earth)", 0))
				run(fixture.Exec("INSERT INTO earth_diag VALUES (1,'6378168'),(2,'(6378168,0)')", 2))
				baseline := earthSelect(b, "SELECT id,v FROM earth_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(6378168)"}, {int64(2), "(6378168, 0)"}})
				base := earthCheckDiagnostic("public", tc.constraint, true)
				for _, prepared := range []bool{false, true} {
					for _, route := range []struct {
						prefix, suffix string
						selectRows     bool
					}{
						{"/*é*/ SELECT ", "::earth AS v", true},
						{"/*é*/ SELECT CAST(", " AS earth) AS v", true},
						{"/*é*/ INSERT INTO earth_diag VALUES (3,'6378168'),(4,", ")", false},
					} {
						cubeError(t, p, b, c, route.prefix+cubeLiteral(tc.input)+route.suffix, prepared, route.selectRows, false, base)
						run(baseline)
					}
					for _, route := range []struct {
						q         string
						parameter int
						args      []any
					}{
						{"INSERT INTO earth_diag VALUES (3,$1)", 1, []any{tc.input}},
						{"INSERT INTO earth_diag VALUES (3,$1),(4,$2)", 2, []any{"6378168", tc.input}},
						{"UPDATE earth_diag SET v=CASE WHEN id=1 THEN $1::earth ELSE $2::earth END", 2, []any{"6378174", tc.input}},
						{"INSERT INTO earth_diag VALUES (3,$1::cube)", 0, []any{tc.input}},
					} {
						want := base
						if route.parameter != 0 {
							want = cubeParameterDiagnostic(want, tc.input, route.parameter)
						}
						cubeError(t, p, b, c, route.q, prepared, false, false, want, route.args...)
						run(baseline)
					}
				}
				q := "INSERT INTO earth_diag VALUES (3,$1),(4,$2)"
				held := earthHeld(t, p, c, q)
				defer held.Statement.Close()
				s := fixture.ErrorDiagnostic(q, cubeParameterDiagnostic(base, tc.input, 2), "6378168", tc.input)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(baseline)
			})
		})
	}
	for _, tc := range []struct{ input, detail string }{
		{"(6378168,)", `syntax error at or near ")"`},
		{"(6378168,0),(6378168)", "Different point dimensions in (6378168,0) and (6378168)."},
		{"1e999", ""},
	} {
		t.Run("cube_parser_"+tc.input, func(t *testing.T) {
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				base := cubeSyntaxDiagnostic(tc.detail)
				if tc.detail == "" {
					base = fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`)
				}
				for _, prepared := range []bool{false, true} {
					prefix := "/*é*/ SELECT "
					want := base
					want.Position = strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
					cubeError(t, p, b, c, prefix+cubeLiteral(tc.input)+"::earth AS v", prepared, true, true, want)
					cubeError(t, p, b, c, "SELECT $1::earth AS v", prepared, true, false, cubeParameterDiagnostic(base, tc.input, 1), tc.input)
				}
			})
		})
	}
	t.Run("column_not_null", func(t *testing.T) {
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			earthInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE earth_required(id integer,v earth NOT NULL)", 0))
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "earth_required" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "earth_required", "v"
			for _, prepared := range []bool{false, true} {
				cubeError(t, p, b, c, "INSERT INTO earth_required VALUES (1,'6378168'),(2,NULL)", prepared, false, false, want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, earthSelect(b, "SELECT v FROM earth_required", []string{"v"}, [][]any{}))
			}
		})
	})
}
