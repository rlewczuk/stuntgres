package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathQueryBindError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message, token, hint string) {
	t.Helper()
	for _, prefix := range []string{"", "/*é🙂*/ "} {
		sql := prefix + q
		d := fixture.ControlsReferenceDiagnostic(code, message)
		d.Hint = hint
		d.Position = fixture.Position(len([]rune(sql[:strings.Index(sql, token)])) + 1)
		s := fixture.ErrorDiagnostic(sql, d)
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		s = fixture.Prepared(s)
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceJSONPathQueryArguments(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpq_doc AS jsonb`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpq_path AS jsonpath`, 0))
		for _, f := range jsonpathQueryFunctions {
			want := any(true)
			if f.typ == "JSONB" {
				want = `true`
			}
			for _, args := range []string{`'true','$'`, `'true','$','{}'`, `'true','$','{}',false`, `path=>'$',target=>'true'`, `'true',path:='$',silent:=false`, `'true'::jpq_doc,'$'::jpq_path`, `'true',VARIADIC '$'`, `'true','$','{}',VARIADIC false`, `target=>'true',VARIADIC path=>'$'`} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+f.name+`(`+args+`) AS changed`, f.typ, want))
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT pg_catalog."`+f.name+`"('true','$') AS changed`, f.typ, want))
			jsonpathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+f.name+`('true','$',silent=>true) AS changed`, f.typ, want))
			for _, tc := range []struct{ args, sig string }{
				{`'1'`, `unknown`}, {`'1','$','{}',false,false`, `unknown, unknown, unknown, boolean, boolean`}, {`'1'::text,'$'`, `text, unknown`}, {`'1','$'::text`, `unknown, text`}, {`target=>'1',wrong=>'$'`, `target => unknown, wrong => unknown`}, {`path=>'$',VARIADIC target=>'1'`, `path => unknown, target => unknown`},
			} {
				jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+`(`+tc.args+`)`, "42883", `function `+f.name+`(`+tc.sig+`) does not exist`, f.name, `No function matches the given name and argument types. You might need to add explicit type casts.`)
			}
			for _, tc := range []struct{ args, message, token string }{
				{`target=>'true',target=>'false',path=>'$'`, `argument name "target" used more than once`, `target=>'false'`},
				{`target=>'true','$'`, `positional argument cannot follow named argument`, `'$'`},
			} {
				jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+`(`+tc.args+`)`, "42601", tc.message, tc.token, "")
			}
			for _, tc := range []struct{ call, message string }{
				{`(DISTINCT 'true','$')`, `DISTINCT specified, but ` + f.name + ` is not an aggregate function`},
				{`('true','$' ORDER BY 1)`, `ORDER BY specified, but ` + f.name + ` is not an aggregate function`},
				{`('true','$') FILTER (WHERE true)`, `FILTER specified, but ` + f.name + ` is not an aggregate function`},
				{`('true','$') OVER ()`, `OVER specified, but ` + f.name + ` is not a window function nor an aggregate function`},
			} {
				jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+tc.call, "42809", tc.message, f.name, "")
			}
		}
	})
}

func TestReferenceJSONPathQueryDiagnostics(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpq_bad_child(doc jsonb,v text)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpq_bad_child VALUES(NULL,'bad')`, 1))
		for _, f := range jsonpathQueryFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			for _, tc := range []struct{ doc, path, code, message string }{
				{`{}`, `strict $.x`, "2203A", `JSON object does not contain key "x"`},
				{`1`, `strict $.x`, "2203A", `jsonpath member accessor can only be applied to an object`},
				{`1`, `strict $.*`, "2203C", `jsonpath wildcard member accessor can only be applied to an object`},
				{`1`, `strict $[0]`, "22039", `jsonpath array accessor can only be applied to an array`},
				{`1`, `strict $[*]`, "22039", `jsonpath wildcard array accessor can only be applied to an array`},
				{`[1]`, `strict $[1]`, "22033", `jsonpath array subscript is out of bounds`},
				{`1`, `$v`, "42704", `could not find jsonpath variable "v"`},
			} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, tc.code, tc.message, tc.doc, tc.path, `{}`, false))
			}
			for _, silent := range []bool{false, true} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "v"`, `1`, `$v`, `{}`, silent))
				for _, vars := range []string{`[]`, `null`, `1`, `"x"`} {
					d := fixture.ControlsReferenceDiagnostic("22023", `"vars" argument is not an object`)
					d.Detail = `Jsonpath parameters should be encoded as key-value pairs of "vars" object.`
					s := fixture.ErrorDiagnostic(q, d, `1`, `$`, vars, silent)
					s.SelectRows = true
					s.ErrorStage = "execute"
					jsonpathNavigateBoth(t, p, b, c, s)
				}
			}
			for _, tc := range []struct{ path, code, message string }{{`$[`, "42601", `syntax error at end of jsonpath input`}, {`$ like_regex "("`, "2201B", `invalid regular expression: parentheses () not balanced`}} {
				literal := jsonReferenceQuote(tc.path)
				jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+`(NULL::jsonb,`+literal+`::jsonpath,silent=>true)`, tc.code, tc.message, literal, "")
			}
			// Strict provider NULL handling must not hide ordinary child errors.
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT `+f.name+`(doc,'$','{}',v::boolean) FROM jpq_bad_child`, "22P02", `invalid input syntax for type boolean: "bad"`))
		}
	})
}
