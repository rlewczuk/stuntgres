package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRecoveryControlDiagnostics(t *testing.T) {
	for _, name := range recoveryControlNames {
		t.Run(name, func(t *testing.T) {
			recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				type sample struct { query, code, message, hint string; position int }
				var cases []sample
				arguments := []struct { sql, typ string }{{"1", "integer"}, {"NULL", "unknown"}, {"NULL::text", "text"}, {"1,2", "integer, integer"}, {"x=>1", "x => integer"}}
				if name == "pg_promote" {
					arguments = []struct { sql, typ string }{{"1", "integer"}, {"true,true", "boolean, boolean"}, {"true,1::bigint", "boolean, bigint"}, {"true,1,2", "boolean, integer, integer"}, {"timeout=>1", "timeout => integer"}, {`"WAIT"=>false`, "WAIT => boolean"}}
					duplicate := "SELECT pg_promote(wait=>true,wait=>false)"
					positional := "SELECT pg_promote(wait=>true,1)"
					cases = append(cases,
						sample{duplicate, "42601", `argument name "wait" used more than once`, "", strings.LastIndex(duplicate, "wait=>")+1},
						sample{positional, "42601", "positional argument cannot follow named argument", "", strings.LastIndex(positional, "1")+1},
						sample{"SELECT pg_promote('bad')", "22P02", `invalid input syntax for type boolean: "bad"`, "", 19},
						sample{"SELECT pg_promote(true,'bad')", "22P02", `invalid input syntax for type integer: "bad"`, "", 24},
					)
				}
				for _, arg := range arguments { cases = append(cases, sample{"SELECT "+name+"("+arg.sql+")", "42883", "function "+name+"("+arg.typ+") does not exist", hint, 8}) }
				cases = append(cases,
					sample{"SELECT public."+name+"()", "42883", "function public."+name+"() does not exist", hint, 8},
					sample{`SELECT "`+strings.ToUpper(name)+`"()`, "42883", "function "+strings.ToUpper(name)+"() does not exist", hint, 8},
					sample{"SELECT missing_recovery_schema."+name+"()", "3F000", `schema "missing_recovery_schema" does not exist`, "", 8},
					sample{"SELECT "+name+"(missing_recovery_argument)", "42703", `column "missing_recovery_argument" does not exist`, "", 9+len(name)},
					sample{"SELECT * FROM "+name+"(1)", "42883", "function "+name+"(integer) does not exist", hint, 15},
					sample{"/*é🙂*/ SELECT "+name+"(1)", "42883", "function "+name+"(integer) does not exist", hint, 15},
					sample{"SELECT "+name+"(*)", "42809", name+"(*) specified, but "+name+" is not an aggregate function", "", 8},
					sample{"SELECT "+name+"() OVER ()", "42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", "", 8},
				)
				for _, tc := range cases {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(tc.position)})
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared { s.ErrorStage = "prepare" }
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		})
	}
}
