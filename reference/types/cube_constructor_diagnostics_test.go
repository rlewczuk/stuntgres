package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type cubeConstructorFailure struct {
	name, expression, parameterExpression string
	args, good                            []any
	want                                  fixture.Diagnostic
}

func cubeConstructorFailures() []cubeConstructorFailure {
	nullElement := cubeConstructorDiagnostic("2202E", "cannot work with arrays containing NULLs", "")
	var cases []cubeConstructorFailure
	addArrays := func(name, first, second string, want fixture.Diagnostic) {
		expr, params := "cube("+cubeLiteral(first)+"::double precision[]", "cube($1::double precision[]"
		args, good := []any{first}, []any{"{1,2}"}
		if second != "" {
			expr += "," + cubeLiteral(second) + "::double precision[]"
			params += ",$2::double precision[]"
			args, good = append(args, second), append(good, "{3,4}")
		}
		cases = append(cases, cubeConstructorFailure{name, expr + ")", params + ")", args, good, want})
	}
	addArrays("single_null", "{1,NULL}", "", nullElement)
	addArrays("first_null", "{1,NULL}", "{2,3}", nullElement)
	addArrays("second_null", "{1,2}", "{3,NULL}", nullElement)
	addArrays("mismatch", "{1,2}", "{3}", cubeConstructorMismatch())
	addArrays("mismatch_reversed", "{1}", "{2,3}", cubeConstructorMismatch())
	addArrays("empty_mismatch", "{}", "{1}", cubeConstructorMismatch())
	addArrays("single_101", cubeConstructorArray(101, "1"), "", cubeConstructorLimit("array is too long"))
	addArrays("first_101", cubeConstructorArray(101, "1"), cubeConstructorArray(101, "2"), cubeConstructorLimit("can't extend cube"))
	addArrays("first_limit_before_mismatch", cubeConstructorArray(101, "1"), "{2}", cubeConstructorLimit("can't extend cube"))
	addArrays("second_101_mismatch", "{1}", cubeConstructorArray(101, "2"), cubeConstructorMismatch())
	addArrays("first_100_second_101", cubeConstructorArray(100, "1"), cubeConstructorArray(101, "2"), cubeConstructorMismatch())
	addArrays("single_null_before_limit", "{"+strings.Repeat("1,", 100)+"NULL}", "", nullElement)
	addArrays("first_null_before_mismatch", "{NULL}", "{1,2}", nullElement)
	addArrays("second_null_before_mismatch", "{1,2}", "{NULL}", nullElement)
	addArrays("second_null_before_first_limit", cubeConstructorArray(101, "1"), "{NULL}", nullElement)
	addArrays("first_null_before_limit_and_mismatch", "{"+strings.Repeat("1,", 100)+"NULL}", "{2}", nullElement)
	for _, two := range []bool{false, true} {
		name, suffix, params := "extend_100", ",2)", ",$2::double precision)"
		args, good := []any{cubeConstructorPoint(100, "1"), 2}, []any{"(1)", 2}
		if two {
			name, suffix, params = "extend_100_two", ",2,3)", ",$2::double precision,$3::double precision)"
			args, good = append(args, 3), append(good, 3)
		}
		cases = append(cases, cubeConstructorFailure{name, "cube(" + cubeLiteral(cubeConstructorPoint(100, "1")) + "::cube" + suffix, "cube($1::cube" + params, args, good, cubeConstructorLimit("can't extend cube")})
	}
	return cases
}

func TestReferenceCubeConstructorDiagnostics(t *testing.T) {
	for _, tc := range cubeConstructorFailures() {
		t.Run(tc.name, func(t *testing.T) {
			cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE cube_constructor_diag(id integer NOT NULL,v cube)", 0))
				run(fixture.Exec("INSERT INTO cube_constructor_diag VALUES (1,cube(7)),(2,cube(8))", 2))
				baseline := cubeSelect(b, "SELECT id,v FROM cube_constructor_diag ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(7)"}, {int64(2), "(8)"}})
				// Runtime-only optionals/stages are discovery hypotheses. Parse and
				// Describe need not plan/evaluate immutable function constants. All
				// optional diagnostic fields are explicitly absent unless settled otherwise.
				for _, prepared := range []bool{false, true} {
					for _, route := range []struct {
						q     string
						query bool
						args  []any
					}{
						{"/*é*/ SELECT " + tc.expression + " AS v", true, nil},
						{"/*é*/ SELECT " + tc.parameterExpression + " AS v", true, tc.args},
						{"INSERT INTO cube_constructor_diag VALUES (3,cube(3)),(4," + tc.expression + ")", false, nil},
						{"INSERT INTO cube_constructor_diag VALUES (3,cube(3)),(4," + tc.parameterExpression + ")", false, tc.args},
						{"UPDATE cube_constructor_diag SET v=CASE WHEN id=1 THEN cube(3) ELSE " + tc.parameterExpression + " END", false, tc.args},
					} {
						cubeError(t, p, b, c, route.q, prepared, route.query, false, tc.want, route.args...)
						run(baseline)
					}
				}
				q := "SELECT " + tc.parameterExpression + " AS v"
				held := cubeConstructorHeld(t, p, c, q)
				defer held.Statement.Close()
				// A genuinely parameter-dependent statement succeeds on both sides
				// of the failure, proving it is not an unusable failed-Prepare handle.
				goodText := "(1, 2)"
				if len(tc.good) == 2 {
					goodText = "(1, 2),(3, 4)"
				}
				if strings.HasPrefix(tc.name, "extend") {
					goodText = "(1, 2)"
					if len(tc.good) == 3 {
						goodText = "(1, 2),(1, 3)"
					}
				}
				good := cubeConstructorSelect(b, q, []string{"v"}, []string{"CUBE"}, [][]any{{goodText}}, tc.good...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				bad := fixture.ErrorDiagnostic(q, tc.want, tc.args...)
				bad.SelectRows, bad.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				run(baseline)
			})
		})
	}
	t.Run("input_producers", func(t *testing.T) {
		cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			for _, tc := range []struct {
				expression, parameter, input string
				want                         fixture.Diagnostic
			}{
				{"cube('{oops}'::double precision[])", "cube($1::double precision[])", "{oops}", cubeConstructorDiagnostic("22P02", `invalid input syntax for type double precision: "oops"`, "")},
				{"cube('(1,)'::cube,2)", "cube($1::cube,2)", "(1,)", cubeSyntaxDiagnostic(`syntax error at or near ")"`)},
			} {
				for _, prepared := range []bool{false, true} {
					literal := tc.want
					literal.Position = "19" // /*é*/ SELECT cube( then the input quote, in characters.
					cubeError(t, p, b, c, "/*é*/ SELECT "+tc.expression+" AS v", prepared, true, true, literal)
					bound := cubeParameterDiagnostic(tc.want, tc.input, 1)
					cubeError(t, p, b, c, "SELECT "+tc.parameter+" AS v", prepared, true, false, bound, tc.input)
				}
			}
		})
	})
}
