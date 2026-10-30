package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func cubeSpatialPosition(prefix string) string {
	return strconv.Itoa(utf8.RuneCountInString(prefix) + 1)
}

func TestReferenceCubeSpatialDiagnostics(t *testing.T) {
	t.Run("input_and_atomicity", func(t *testing.T) {
		cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeSpatialSeed(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cube_spatial_atomic(id integer,a cube,b cube,hit boolean,d float8)", 0))
			run(fixture.Exec("INSERT INTO cube_spatial_atomic VALUES (1,'(3,4)','()',false,5),(2,'(0)','(0)',true,0)", 2))
			baseline := fixture.Select("SELECT id,a::text AS a,b::text AS b,hit,d FROM cube_spatial_atomic ORDER BY id", []string{"id", "a", "b", "hit", "d"}, []string{"INT4", "TEXT", "TEXT", "BOOL", "FLOAT8"}, [][]any{{int64(1), "(3, 4)", "()", false, float64(5)}, {int64(2), "(0)", "(0)", true, float64(0)}})
			cases := []struct {
				name, input string
				diagnostic  fixture.Diagnostic
			}{
				{"syntax", "(1,)", cubeSyntaxDiagnostic(`syntax error at or near ")"`)},
				{"dimensions", "(1,2),(3)", cubeSyntaxDiagnostic("Different point dimensions in (1,2) and (3).")},
				{"limit101", "(" + strings.Repeat("1,", 100) + "1)", cubeSyntaxDiagnostic("A cube cannot have more than 100 dimensions.")},
				{"range", "1e999", fixture.ControlsReferenceDiagnostic("22003", `"1e999" is out of range for type double precision`)},
			}
			for _, tc := range cases {
				for _, surface := range cubeSpatialSurfaces {
					t.Run(tc.name+"/"+surface, func(t *testing.T) {
						for _, prepared := range []bool{false, true} {
							for _, other := range []string{"'(0)'::cube", "NULL::cube"} {
								prefix := "/*é*/ SELECT "
								if surface == "cube_distance" {
									prefix += "cube_distance("
								}
								q := "/*é*/ SELECT " + cubeSpatialExpression(surface, cubeLiteral(tc.input)+"::cube", other) + " AS result"
								want := tc.diagnostic
								want.Position = cubeSpatialPosition(prefix)
								cubeError(t, p, b, c, q, prepared, true, true, want)
							}
							bound := "SELECT " + cubeSpatialExpression(surface, "$1::cube", "$2::cube") + " AS result"
							cubeError(t, p, b, c, bound, prepared, true, false, cubeParameterDiagnostic(tc.diagnostic, tc.input, 1), tc.input, "(0)")
						}
					})
				}
				t.Run(tc.name+"/atomic", func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						// The first tuple is valid and must not survive later input failure.
						prefix := "/*é*/ INSERT INTO cube_spatial_atomic VALUES (3,'(1)','(1)',true,0),(4,'(1)','(1)',true,cube_distance("
						want := tc.diagnostic
						want.Position = cubeSpatialPosition(prefix)
						cubeError(t, p, b, c, prefix+cubeLiteral(tc.input)+"::cube,'(0)'::cube))", prepared, false, true, want)
						run(baseline)
						q := "INSERT INTO cube_spatial_atomic VALUES (3,$1::cube,'(1)',true,cube_distance($1::cube,'(1)'::cube)),(4,$2::cube,'(1)',true,cube_distance($2::cube,'(1)'::cube))"
						cubeError(t, p, b, c, q, prepared, false, false, cubeParameterDiagnostic(tc.diagnostic, tc.input, 2), "(1)", tc.input)
						run(baseline)
						q = "UPDATE cube_spatial_atomic SET hit=(CASE WHEN id=1 THEN $1::cube ELSE $2::cube END) && b,d=cube_distance(a,CASE WHEN id=1 THEN $1::cube ELSE $2::cube END)"
						cubeError(t, p, b, c, q, prepared, false, false, cubeParameterDiagnostic(tc.diagnostic, tc.input, 2), "(1)", tc.input)
						run(baseline)
					}
				})
			}
		})
	})
	t.Run("signature_and_visibility", func(t *testing.T) {
		cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			// Root's pinned PG18.6 oracle confirmed these independently specified
			// complete signature/location records before native implementation.
			cases := []struct{ name, query, message, hint, token, code string }{
				{"function_integer", "/*é*/ SELECT cube_distance(1,2)", "function cube_distance(integer, integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_distance", "42883"},
				{"function_arity", "SELECT cube_distance('(0)'::cube)", "function cube_distance(cube) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_distance", "42883"},
				{"function_text", "SELECT cube_distance('(0)'::text,'(1)'::cube)", "function cube_distance(text, cube) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "cube_distance", "42883"},
				{"operator_integer", "/*é*/ SELECT '(0)'::cube && 1", "operator does not exist: cube && integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "&&", "42883"},
				{"operator_text", "SELECT '(0)'::cube <-> '(1)'::text", "operator does not exist: cube <-> text", "No operator matches the given name and argument types. You might need to add explicit type casts.", "<->", "42883"},
				{"typed_null_wrong_signature", "SELECT NULL::cube <#> 1", "operator does not exist: cube <#> integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "<#>", "42883"},
				{"missing_namespace", "SELECT cube_spatial_missing.cube_distance('(0)'::cube,'(1)'::cube)", `schema "cube_spatial_missing" does not exist`, "", "cube_spatial_missing", "3F000"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Hint = tc.hint
					want.Position = cubeSpatialPosition(tc.query[:strings.Index(tc.query, tc.token)])
					for _, prepared := range []bool{false, true} {
						cubeError(t, p, b, c, tc.query, prepared, true, true, want)
					}
				})
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=pg_catalog", 0))
			for _, tc := range []struct{ q, message, token, hint string }{
				{"SELECT cube_distance('(0)'::public.cube,'(1)'::public.cube)", "function cube_distance(public.cube, public.cube) does not exist", "cube_distance", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT '(0)'::public.cube && '(1)'::public.cube", "operator does not exist: public.cube && public.cube", "&&", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			} {
				t.Run("hidden_"+tc.token, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
					want.Hint, want.Position = tc.hint, cubeSpatialPosition(tc.q[:strings.Index(tc.q, tc.token)])
					for _, prepared := range []bool{false, true} {
						cubeError(t, p, b, c, tc.q, prepared, true, true, want)
					}
				})
			}
		})
	})
	t.Run("uninstalled", func(t *testing.T) {
		cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			want := fixture.ControlsReferenceDiagnostic("42883", "function cube_distance(integer, integer) does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				cubeError(t, p, b, c, "SELECT cube_distance(1,2)", prepared, true, true, want)
			}
		})
	})
}
