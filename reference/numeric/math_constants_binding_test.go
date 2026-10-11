package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathConstantsBinding(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range referenceMathConstantsUnaryNames {
			want := float64(0)
			input := "0"
			if name == "exp" || name == "erfc" {
				want = 1
			}
			if name == "gamma" {
				input, want = "1", 1
			}
			for _, typ := range []string{"smallint", "integer", "bigint", "real", "double precision", "numeric"} {
				resultType := "FLOAT8"
				var value any = want
				if name == "exp" && typ == "numeric" {
					resultType, value = "NUMERIC", "1.0000000000000000"
				}
				s := fixture.Select("SELECT "+name+"("+input+"::"+typ+") AS result", []string{"result"}, []string{resultType}, [][]any{{value}})
				referenceMathTrigStep(t, p, backend, s)
			}
			for _, spelling := range []string{name, strings.ToUpper(name), "pg_catalog." + name, `"` + name + `"`} {
				referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+spelling+"('"+input+"') AS result", []string{"result"}, []string{"FLOAT8"}, [][]any{{want}}))
			}
		}
		for _, spelling := range []string{"pi", "PI", "pg_catalog.pi", `"pi"`} {
			referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+spelling+"() AS result", []string{"result"}, []string{"FLOAT8"}, [][]any{{3.141592653589793}}))
		}
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range append([]string{"pi"}, referenceMathConstantsUnaryNames...) {
			cases := []struct{ spelling, args, signature string }{
				{name, "TRUE", "boolean"}, {name, "'0'::text", "text"}, {name, "NULL::text", "text"},
				{name, "x => 0", "x => integer"}, {name, "VARIADIC ARRAY[1]", "integer[]"},
				{"public." + name, "1", "integer"}, {`"` + strings.ToUpper(name) + `"`, "1", "integer"},
			}
			if name == "pi" {
				cases = append(cases, struct{ spelling, args, signature string }{name, "1", "integer"})
			} else {
				cases = append(cases, struct{ spelling, args, signature string }{name, "", ""}, struct{ spelling, args, signature string }{name, "1,1", "integer, integer"})
			}
			for _, tc := range cases {
				t.Run(tc.spelling+"("+tc.args+")", func(t *testing.T) {
					q := "SELECT " + tc.spelling + "(" + tc.args + ")"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function " + strings.ReplaceAll(tc.spelling, `"`, "") + "(" + tc.signature + ") does not exist", Position: "8", Hint: hint})
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
				})
			}
			if name == "pi" {
				continue
			}
			for _, prefix := range []string{"", "/*é*/ "} {
				s := fixture.ImplicitError(prefix+"SELECT "+name+"('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type double precision: "oops"`, Position: strconv.Itoa(9 + len(name) + len([]rune(prefix)))})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			}
		}
	})
}

func TestReferenceMathConstantsMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE constants_metadata(d DOUBLE PRECISION)", 0))
	p.Step(t, fixture.Exec("INSERT INTO constants_metadata VALUES(NULL)", 1))
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range append([]string{"pi"}, referenceMathConstantsUnaryNames...) {
			expr, value := name+"(d)", any(nil)
			if name == "pi" {
				expr, value = "pi()", 3.141592653589793
			}
			for _, empty := range []bool{false, true} {
				q := "SELECT " + expr + " AS result FROM constants_metadata"
				rows := [][]any{{value}}
				if empty {
					q += " WHERE FALSE"
					rows = nil
				}
				s := fixture.Select(q, []string{"result"}, []string{"FLOAT8"}, rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
				referenceMathTrigStep(t, p, backend, s)
			}
		}
	})
}
