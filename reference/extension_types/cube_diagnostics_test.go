package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func cubeSyntaxDiagnostic(detail string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for cube")
	want.Detail = detail
	return want
}

func TestReferenceCubeDiagnostics(t *testing.T) {
	cases := []struct{ input, detail string }{
		{"", "syntax error at end of input"}, {" ", "syntax error at end of input"},
		{"ABC", `syntax error at or near "A"`}, {"[]", `syntax error at or near "]"`},
		{"[()]", `syntax error at or near "]"`}, {"[(1),]", `syntax error at or near "]"`},
		{"(1,)", `syntax error at or near ")"`}, {"1,,2", `syntax error at or near ","`},
		{"(1,2)a", `syntax error at or near "a"`}, {"1..2", `syntax error at or near ".2"`},
		{"1 e7", `syntax error at or near "e"`}, {"(1", "syntax error at end of input"},
		{"+NaN", `syntax error at or near "+"`}, {"-NaN", `syntax error at or near "-"`},
		{"0x10", `syntax error at or near "x"`}, {"NaN(payload)", `syntax error at or near "("`},
		{"(1,2),(3)", "Different point dimensions in (1,2) and (3)."},
		{"(" + strings.Repeat("1,", 100) + "1)", "A cube cannot have more than 100 dimensions."},
		{"(" + strings.Repeat("1,", 100) + "1),(" + strings.Repeat("2,", 100) + "2)", "A cube cannot have more than 100 dimensions."},
		{"(1,2),(3)junk", "Different point dimensions in (1,2) and (3)."},
		{"(" + strings.Repeat("1,", 100) + "1)junk", "A cube cannot have more than 100 dimensions."},
		{"1e999", ""}, {"-1e-700", ""},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE cube_diag(id integer NOT NULL,v cube)", 0))
				run(fixture.Exec("INSERT INTO cube_diag VALUES (1,'1'),(2,'(2),(3)')", 2))
				base := cubeSyntaxDiagnostic(tc.detail)
				if tc.detail == "" {
					base = fixture.ControlsReferenceDiagnostic("22003", `"`+tc.input+`" is out of range for type double precision`)
				}
				baseline := fixture.CubeSelect(b, "SELECT id,v FROM cube_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(1)"}, {int64(2), "(2),(3)"}})
				for _, prepared := range []bool{false, true} {
					for _, route := range []struct {
						prefix, suffix string
						selectRows     bool
					}{
						{"/*é*/ SELECT ", "::cube AS v", true},
						{"/*é*/ SELECT CAST(", " AS cube) AS v", true},
						{"/*é*/ INSERT INTO cube_diag VALUES (3,'3'),(4,", ")", false},
					} {
						want := base
						want.Position = strconv.Itoa(utf8.RuneCountInString(route.prefix) + 1)
						cubeError(t, p, b, c, route.prefix+cubeLiteral(tc.input)+route.suffix, prepared, route.selectRows, true, want)
						run(baseline)
					}
					for _, route := range []struct {
						q         string
						parameter int
						args      []any
					}{
						{"INSERT INTO cube_diag VALUES (3,$1)", 1, []any{tc.input}},
						{"INSERT INTO cube_diag VALUES (3,$1),(4,$2)", 2, []any{"3", tc.input}},
						{"UPDATE cube_diag SET v=CASE WHEN id=1 THEN $1::cube ELSE $2::cube END", 2, []any{"3", tc.input}},
					} {
						want := cubeParameterDiagnostic(base, tc.input, route.parameter)
						cubeError(t, p, b, c, route.q, prepared, false, false, want, route.args...)
						run(baseline)
					}
				}
			})
		})
	}
	t.Run("declaration_modifiers", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			for _, typ := range []string{"cube", "public.cube"} {
				want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "`+typ+`"`)
				want.Position = "30"
				for _, prepared := range []bool{false, true} {
					cubeError(t, p, b, c, "CREATE TABLE cube_bad_type(v "+typ+"(2))", prepared, false, false, want)
				}
			}
		})
	})
}

func TestReferenceCubeDiagnosticPrecedence(t *testing.T) {
	// Release grammar expectations confirmed by the pinned PostgreSQL 18.6 oracle.
	// A box production completes before dimension checks and float conversion;
	// normalized diagnostic lists retain numeric token spelling, not whitespace.
	const eof = "syntax error at end of input"
	const dimensions = "A cube cannot have more than 100 dimensions."
	const mismatch = "Different point dimensions in (1,2) and (3)."
	overlimit := "(" + strings.Repeat("1,", 100) + "1e999)"
	ordinaryOverlimit := "(" + strings.Repeat("2,", 100) + "2)"
	cases := []struct{ name, input, detail string }{
		{"incomplete_point_overflow", "(1e999", eof},
		{"incomplete_second_corner_overflow", "(1),(1e999", eof},
		{"incomplete_bracketed_point_overflow", "[(1e999", eof},
		{"missing_bracket_overflow", "[(1e999),(2)", eof},
		{"missing_bracket_mismatch", "[(1,2),(3)", eof},
		{"missing_bracket_mismatch_overflow", "[(1e999,2),(3)", eof},
		{"missing_bracket_overlimit_overflow", "[" + overlimit + "," + ordinaryOverlimit, eof},
		{"overlimit_bare_point_overflow", strings.TrimSuffix(strings.TrimPrefix(overlimit, "("), ")"), dimensions},
		{"overlimit_point_overflow", overlimit, dimensions},
		{"overlimit_box_first_overflow", overlimit + "," + ordinaryOverlimit, dimensions},
		{"overlimit_box_second_overflow", ordinaryOverlimit + "," + overlimit, dimensions},
		{"overlimit_bracketed_box_overflow", "[" + overlimit + "," + ordinaryOverlimit + "]", dimensions},
		{"mismatch_spaces", "( 1 , 2 ),( 3 )", mismatch},
		{"mismatch_ascii_whitespace", "[\t(\n1\r,\f2\v ) , (\t3\r\n )\f]", mismatch},
		{"mismatch_token_spelling", "( +01.0e+0 , .20E1 ),( -3. )", "Different point dimensions in (+01.0e+0,.20E1) and (-3.)."},
		{"completed_point_overflow_trailing", "(1e999)junk", ""},
		{"completed_box_overflow_trailing", "(1e999),(2)junk", ""},
		{"completed_bracketed_box_overflow_trailing", "[(1e999),(2)]junk", ""},
		{"completed_mismatch_overflow_trailing", "(1e999,2),(3)junk", "Different point dimensions in (1e999,2) and (3)."},
		{"completed_mismatch_whitespace_trailing", "( 1 , 2 ),( 3 )junk", mismatch},
		{"completed_overlimit_point_overflow_trailing", overlimit + "junk", dimensions},
		{"completed_overlimit_box_overflow_trailing", overlimit + "," + ordinaryOverlimit + "junk", dimensions},
		{"completed_valid_point_trailing", "(1)junk", `syntax error at or near "j"`},
		{"completed_valid_box_trailing", "[(1),(2)]junk", `syntax error at or near "j"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					cubeInstall(t, p, b, c)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE cube_precedence(id integer NOT NULL,v cube)", 0))
					run(fixture.Exec("INSERT INTO cube_precedence VALUES (1,'1'),(2,'(2),(3)')", 2))
					base := cubeSyntaxDiagnostic(tc.detail)
					if tc.detail == "" {
						base = fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`)
					}
					baseline := fixture.CubeSelect(b, "SELECT id,v FROM cube_precedence ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(1)"}, {int64(2), "(2),(3)"}})
					for _, prepared := range []bool{false, true} {
						for _, route := range []struct {
							prefix, suffix string
							selectRows     bool
						}{
							{"/*é*/ SELECT ", "::cube AS v", true},
							{"/*é*/ SELECT CAST(", " AS cube) AS v", true},
							{"/*é*/ INSERT INTO cube_precedence VALUES (3,'3'),(4,", ")", false},
						} {
							want := base
							want.Position = strconv.Itoa(utf8.RuneCountInString(route.prefix) + 1)
							cubeError(t, p, b, c, route.prefix+cubeLiteral(tc.input)+route.suffix, prepared, route.selectRows, true, want)
							run(baseline)
						}
						for _, route := range []struct {
							q         string
							parameter int
							args      []any
						}{
							{"INSERT INTO cube_precedence VALUES (3,$1)", 1, []any{tc.input}},
							{"INSERT INTO cube_precedence VALUES (3,$1),(4,$2)", 2, []any{"3", tc.input}},
							{"UPDATE cube_precedence SET v=CASE WHEN id=1 THEN $1::cube ELSE $2::cube END", 2, []any{"3", tc.input}},
						} {
							want := cubeParameterDiagnostic(base, tc.input, route.parameter)
							cubeError(t, p, b, c, route.q, prepared, false, false, want, route.args...)
							run(baseline)
						}
					}
				})
			}
		})
	}
}
