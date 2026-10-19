package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBPathOpsDiagnostics(t *testing.T) {
	jsonbPathOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jpo_bad_child(doc jsonb,path text)`, 0))
		run(fixture.Exec(`INSERT INTO jpo_bad_child VALUES(NULL,'$[')`, 1))
		for _, op := range jsonbPathOps {
			for _, tc := range []struct{ path, code, message string }{
				{`$[`, "42601", `syntax error at end of jsonpath input`},
				{``, "22P02", `invalid input syntax for type jsonpath: ""`},
				{`$[]`, "42601", `syntax error at or near "]" of jsonpath input`},
				{`$ like_regex "("`, "2201B", `invalid regular expression: parentheses () not balanced`},
				{`1e131072`, "22003", `value overflows numeric format`},
			} {
				t.Run(op+"/"+tc.path, func(t *testing.T) {
					for _, doc := range []string{`'{}'::jsonb`, `NULL::jsonb`} {
						for _, prefix := range []string{"", "/*é*/ "} {
							literal := jsonReferenceQuote(tc.path)
							q := prefix + `SELECT ` + doc + ` ` + op + ` ` + literal + ` AS changed`
							d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
							d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, literal)])) + 1)
							s := fixture.ErrorDiagnostic(q, d)
							s.SelectRows = true
							s.ErrorStage = "execute"
							run(s)
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
							run(s)
						}
					}
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Context = "unnamed portal parameter $2 = '...'"
					if tc.path == "" {
						d.Context = "unnamed portal parameter $2 = ''"
					}
					s := fixture.ErrorDiagnostic(jsonbPathOpsSQL(op), d, `{}`, tc.path)
					s.SelectRows = true
					s.ErrorStage = "execute"
					jsonpathNavigateBoth(t, p, b, c, s)
				})
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonbPathOpsSQL(op), "42704", `could not find jsonpath variable "v"`, `1`, `$v`))
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT doc `+op+` path::jsonpath AS changed FROM jpo_bad_child`, "42601", `syntax error at end of jsonpath input`))
		}
	})
}
