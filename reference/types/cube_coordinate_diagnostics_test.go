package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeCoordinateDiagnostics(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE cube_coordinate_diag(id integer NOT NULL,v float8)", 0))
		run(fixture.Exec("INSERT INTO cube_coordinate_diag VALUES (1,7),(2,8)", 2))
		baseline := fixture.Select("SELECT id,v FROM cube_coordinate_diag ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(7)}, {int64(2), float64(8)}})
		point100 := "(" + strings.TrimSuffix(strings.Repeat("7,", 100), ",") + ")"
		for _, tc := range []struct {
			cube       string
			n          int64
			projection bool
		}{
			{"(3,1),(1,4)", 0, false}, {"(3,1),(1,4)", -1, false}, {"(3,1),(1,4)", 5, false},
			{"(3,1),(1,4)", 2147483647, false}, {"(3,1),(1,4)", -2147483648, false},
			{"()", 1, false}, {"()", 0, false}, {point100, 201, false}, {"(1)", 0, true},
		} {
			message := fmt.Sprintf("cube index %d is out of bounds", tc.n)
			op, proc := "->", "cube_coord"
			if tc.projection {
				op, proc, message = "~>", "cube_coord_llur", "zero cube index is not defined"
			}
			want := fixture.ControlsReferenceDiagnostic("2202E", message)
			for _, expr := range []string{"$1::cube " + op + " $2::integer", proc + "($1::cube,$2::integer)"} {
				literal := strings.ReplaceAll(strings.ReplaceAll(expr, "$1", cubeLiteral(tc.cube)), "$2", fmt.Sprintf("(%d)", tc.n))
				for _, prepared := range []bool{false, true} {
					// Evaluation-stage and absent optional fields are predictions
					// awaiting the root's pinned oracle, not observed assertions.
					for _, route := range []struct {
						q     string
						query bool
						args  []any
					}{
						{"SELECT " + literal + " AS v", true, nil},
						{"SELECT " + expr + " AS v", true, []any{tc.cube, tc.n}},
						{"INSERT INTO cube_coordinate_diag VALUES (3,3),(4," + expr + ")", false, []any{tc.cube, tc.n}},
						{"UPDATE cube_coordinate_diag SET v=CASE WHEN id=1 THEN 3::float8 ELSE " + expr + " END", false, []any{tc.cube, tc.n}},
					} {
						cubeError(t, p, b, c, route.q, prepared, route.query, false, want, route.args...)
						run(baseline)
					}
				}
			}
		}
		q := "SELECT $1::cube -> $2::integer AS v"
		held := cubeCoordinateHeld(t, p, c, q)
		good := cubeCoordinateScalar(b, q, 3, "(3,1),(1,4)", 1)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		bad := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "cube index 0 is out of bounds"), "(1)", 0)
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		for _, prepared := range []bool{false, true} {
			literal := cubeSyntaxDiagnostic(`syntax error at or near ")"`)
			literal.Position = "14" // /*é*/ SELECT then the quote: character, not byte.
			cubeError(t, p, b, c, "/*é*/ SELECT '(1,)'::cube -> NULL::integer", prepared, true, true, literal)
			bound := cubeParameterDiagnostic(cubeSyntaxDiagnostic(`syntax error at or near ")"`), "(1,)", 1)
			cubeError(t, p, b, c, "SELECT $1::cube -> NULL::integer", prepared, true, false, bound, "(1,)")
			for _, tc := range []struct{ input, code, message string }{
				{"oops", "22P02", `invalid input syntax for type integer: "oops"`},
				{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
			} {
				base := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				lit := base
				query := "/*é*/ SELECT NULL::cube ~> " + cubeLiteral(tc.input) + "::integer"
				lit.Position = cubeSpatialPosition(query[:strings.Index(query, "'")])
				cubeError(t, p, b, c, query, prepared, true, true, lit)
				param := cubeParameterDiagnostic(base, tc.input, 1)
				cubeError(t, p, b, c, "SELECT NULL::cube ~> $1::integer", prepared, true, false, param, tc.input)
			}
			cubeSpatialBoth(t, p, b, c, cubeCoordinateSelect(b, "SELECT "+cubeCoordinateProjection("NULL::cube", "0"), [][]any{{nil, nil, nil, nil}}))
		}
		run(baseline)
	})
}
