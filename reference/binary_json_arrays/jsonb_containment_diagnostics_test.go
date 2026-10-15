package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBContainmentDiagnostics(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "jbc_empty")
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA jbc_empty", 0))
		for _, op := range []string{"@>", "<@"} {
			for _, tc := range []struct {
				left, right, lt, rt string
				position            int
			}{
				{"'{}'::jsonb", "1", "jsonb", "integer", 20},
				{"1", "'{}'::jsonb", "integer", "jsonb", 10},
				{"'{}'::jsonb", "'{}'::text", "jsonb", "text", 20},
				{"'{}'::text", "'{}'::jsonb", "text", "jsonb", 19},
				{"'{}'::jsonb", "ARRAY[1]", "jsonb", "integer[]", 20},
				{"ARRAY[1]", "'{}'::jsonb", "integer[]", "jsonb", 17},
				{"'{}'::json", "'{}'::json", "json", "json", 19},
				{"/*é*/ '{}'::jsonb", "1", "jsonb", "integer", 26},
			} {
				q := "SELECT " + tc.left + " " + op + " " + tc.right
				t.Run(q, func(t *testing.T) {
					s := jsonbExistsSignature(q, tc.lt+" "+op+" "+tc.rt, tc.position)
					s.ErrorStage = "execute"
					jsonTypeAnalysisError(t, p, b, c, s)
				})
			}
			t.Run("ambiguous "+op, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown "+op+" unknown")
				d.Position = "13"
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				s := fixture.ErrorDiagnostic("SELECT '{}' "+op+" '{}'", d)
				s.SelectRows, s.ErrorStage = true, "execute"
				jsonTypeAnalysisError(t, p, b, c, s)
			})
			t.Run("hidden namespace "+op, func(t *testing.T) {
				s := jsonbExistsSignature("SELECT '{}'::jsonb OPERATOR(jbc_empty."+op+") '{}'::jsonb", "jsonb jbc_empty."+op+" jsonb", 20)
				s.ErrorStage = "execute"
				jsonTypeAnalysisError(t, p, b, c, s)
			})
			t.Run("missing namespace "+op, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("3F000", `schema "jbc_missing" does not exist`)
				d.Position = "20"
				s := fixture.ErrorDiagnostic("SELECT '{}'::jsonb OPERATOR(jbc_missing."+op+") '{}'::jsonb", d)
				s.SelectRows, s.ErrorStage = true, "execute"
				jsonTypeAnalysisError(t, p, b, c, s)
			})
			for _, operands := range [][2]string{{"'bad'::jsonb", "NULL::jsonb"}, {"NULL::jsonb", "'bad'::jsonb"}} {
				q := "SELECT " + operands[0] + " " + op + " " + operands[1]
				t.Run(q, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail, d.Context = `Token "bad" is invalid.`, "JSON data, line 1: bad"
					d.Position = "8"
					if operands[0] == "NULL::jsonb" {
						d.Position = "23"
					}
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows, s.ErrorStage = true, "execute"
					jsonTypeAnalysisError(t, p, b, c, s)
				})
			}
			for _, parameter := range []int{1, 2} {
				t.Run("bound "+op+fixture.Position(parameter), func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail = `Token "bad" is invalid.`
					d.Context = "JSON data, line 1: bad\nunnamed portal parameter $" + fixture.Position(parameter) + " = '...'"
					args := []any{nil, nil}
					args[parameter-1] = "bad"
					s := fixture.ErrorDiagnostic("SELECT $1::jsonb "+op+" $2::jsonb AS v", d, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					jsonTypeBoth(t, p, b, c, s)
				})
			}
		}
	})
}
