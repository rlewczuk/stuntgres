package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryTZNulls(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					doc, path string
					scalar    any
					set       [][]any
				}{
					{`{"x":null}`, `$.x`, nil, jsonpathQueryRows(`null`)},
					{`{"x":false}`, `$.x`, false, jsonpathQueryRows(`false`)},
					{`{}`, `$.missing`, nil, nil},
				} {
					rows := tc.set
					switch f.name {
					case "jsonb_path_exists_tz":
						rows = jsonpathQueryRows(tc.doc != `{}`)
					case "jsonb_path_match_tz":
						rows = jsonpathQueryRows(tc.scalar)
					case "jsonb_path_query_first_tz":
						if len(rows) == 0 {
							rows = jsonpathQueryRows(nil)
						}
					case "jsonb_path_query_array_tz":
						v := `[]`
						if tc.doc == `{"x":null}` {
							v = `[null]`
						}
						if tc.doc == `{"x":false}` {
							v = `[false]`
						}
						rows = jsonpathQueryRows(v)
					}
					s := jsonpathQueryResult(q, f.typ, rows, tc.doc, tc.path, `{}`, true)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				for slot := range 4 {
					args := []any{`true`, `$`, `{}`, false}
					args[slot] = nil
					rows := jsonpathQueryRows(nil)
					if f.set {
						rows = nil
					}
					s := jsonpathQueryResult(q, f.typ, rows, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}()
		}
	})
}

func TestReferenceJSONPathQueryTZSilent(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":true},{},{"x":false}]`, `strict $[*].x`, `{}`, false))
			want := any(`true`)
			if f.name == "jsonb_path_match_tz" {
				want = true
			}
			if f.name == "jsonb_path_exists_tz" {
				want = nil
			}
			if f.name == "jsonb_path_query_array_tz" {
				want = `[true]`
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(q, f.typ, want, `[{"x":true},{},{"x":false}]`, `strict $[*].x`, `{}`, true))
		}
		for _, doc := range []string{`{}`, `{"x":1}`, `{"x":[true,false]}`} {
			path := `$.x`
			if doc == `{"x":[true,false]}` {
				path = `$.x[*]`
			}
			q := `SELECT jsonb_path_match_tz($1,$2,$3,$4) AS changed`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "22038", "single boolean result is expected", doc, path, `{}`, false))
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(q, "BOOL", nil, doc, path, `{}`, true))
		}
	})
}

func TestReferenceJSONPathQueryTZDiagnostics(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpqtz_bad_child(doc jsonb,v text)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpqtz_bad_child VALUES(NULL,'bad')`, 1))
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			for _, silent := range []bool{false, true} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, "42704", `could not find jsonpath variable "name"`, `1`, `$name`, `{}`, silent))
				for _, vars := range []string{`[]`, `null`, `1`, `"x"`} {
					d := fixture.ControlsReferenceDiagnostic("22023", `"vars" argument is not an object`)
					d.Detail = `Jsonpath parameters should be encoded as key-value pairs of "vars" object.`
					s := fixture.ErrorDiagnostic(q, d, `true`, `$`, vars, silent)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.JSONPathNavigateBoth(t, p, b, c, s)
				}
			}
			d := fixture.ControlsReferenceDiagnostic("22031", `datetime format is not recognized: "bad"`)
			d.Hint = `Use a datetime template argument to specify the input data format.`
			s := fixture.ErrorDiagnostic(q, d, `"bad"`, `$.datetime()`, `{}`, false)
			s.SelectRows = true
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
			d = fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of jsonpath input")
			d.Context = "unnamed portal parameter $2 = '...'"
			s = fixture.ErrorDiagnostic(q, d, `true`, `$[`, `{}`, true)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
			literal := fixture.JSONReferenceQuote(`$[`)
			jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+`(NULL::jsonb,`+literal+`::jsonpath,silent=>true)`, "42601", "syntax error at end of jsonpath input", literal, "")
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT `+f.name+`(doc,'$','{}',v::boolean) FROM jpqtz_bad_child`, "22P02", `invalid input syntax for type boolean: "bad"`))
		}
	})
}

func TestReferenceJSONPathQueryTZArguments(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpqtz_doc AS jsonb`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jpqtz_path AS jsonpath`, 0))
		for _, f := range jsonpathQueryTZFunctions {
			want := any(true)
			if f.typ == "JSONB" {
				want = `true`
			}
			if f.name == "jsonb_path_query_array_tz" {
				want = `[true]`
			}
			for _, args := range []string{`'true','$'`, `'true','$','{}'`, `'true','$','{}',false`, `path=>'$',target=>'true'`, `'true',path:='$',silent:=false`, `'true'::jpqtz_doc,'$'::jpqtz_path`, `'true',VARIADIC '$'`, `'true','$','{}',VARIADIC false`, `target=>'true',VARIADIC path=>'$'`, `'true','$',silent=>true`} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+f.name+`(`+args+`) AS changed`, f.typ, want))
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT pg_catalog."`+f.name+`"('true','$') AS changed`, f.typ, want))
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathQueryOne(`SELECT `+f.name+`($1,$2,$3,$4) AS changed`, f.typ, want, `true`, `$`, `{}`, false))
		}
	})
}

func TestReferenceJSONPathQueryTZGrammarBinding(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryTZFunctions {
			for _, tc := range []struct{ args, sig string }{
				{`'1'`, `unknown`}, {`'1','$','{}',false,false`, `unknown, unknown, unknown, boolean, boolean`}, {`'1'::text,'$'`, `text, unknown`}, {`'1','$'::text`, `unknown, text`}, {`target=>'1',wrong=>'$'`, `target => unknown, wrong => unknown`},
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
			q := `SELECT ` + f.name + `($1,$1)`
			d := fixture.ControlsReferenceDiagnostic("42P08", `inconsistent types deduced for parameter $1`)
			d.Detail = "jsonb versus jsonpath"
			d.Position = fixture.Position(len([]rune(`SELECT `+f.name+`($1,`)) + 1)
			s := fixture.Prepared(fixture.ErrorDiagnostic(q, d, `true`))
			s.SelectRows = true
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			jsonpathQueryBindError(t, p, b, c, `SELECT `+f.name+`('true','$'); unexpected`, "42601", `syntax error at or near "unexpected"`, `unexpected`, "")
		}
	})
}
