package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONQueryDiagnostics(t *testing.T) {
	sqljsonQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE sqjq_runtime_inputs(id int,doc jsonb,path text,binding text)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO sqjq_runtime_inputs VALUES(1,NULL,'$[','bad'),(2,'{}',NULL,'bad'),(3,'{}','$[','true'),(4,'{}','$','bad')`, 4))
		for _, tc := range []struct{ expr, doc, path, code, message, hint, detail string }{
			{`JSON_QUERY($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `[1,2]`, `$[*]`, "22034", `JSON path expression in JSON_QUERY must return single item when no wrapper is requested`, `Use the WITH WRAPPER clause to wrap SQL/JSON items into an array.`, ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `[1,2]`, `$[*]`, "22034", `JSON path expression in JSON_VALUE must return single scalar item`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `{}`, `$`, "2203F", `JSON path expression in JSON_VALUE must return single scalar item`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `[]`, `$`, "2203F", `JSON path expression in JSON_VALUE must return single scalar item`, "", ""},
			{`JSON_QUERY($1::jsonb,$2::jsonpath ERROR ON EMPTY)`, `{}`, `$.x`, "22035", `no SQL/JSON item found for specified path`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON EMPTY)`, `{}`, `$.x`, "22035", `no SQL/JSON item found for specified path`, "", ""},
			{`JSON_EXISTS($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `{}`, `strict $.x`, "2203A", `JSON object does not contain key "x"`, "", ""},
			{`JSON_QUERY($1::jsonb,$2::jsonpath WITH WRAPPER ERROR ON ERROR)`, `[{"x":1},{}]`, `strict $[*].x`, "2203A", `JSON object does not contain key "x"`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath ERROR ON ERROR)`, `[{"x":1},{}]`, `strict $[*].x`, "2203A", `JSON object does not contain key "x"`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath RETURNING int ERROR ON ERROR)`, `"aaa"`, `$`, "22P02", `invalid input syntax for type integer: "aaa"`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath RETURNING int ERROR ON ERROR)`, `1.25`, `$`, "22P02", `invalid input syntax for type integer: "1.25"`, "", ""},
			{`JSON_VALUE($1::jsonb,$2::jsonpath RETURNING char(3) ERROR ON ERROR)`, `"abcd"`, `$`, "22001", `value too long for type character(3)`, "", ""},
			{`JSON_QUERY($1::jsonb,$2::jsonpath RETURNING bigint[] EMPTY OBJECT ON ERROR)`, `1`, `strict $.x`, "42804", `could not coerce ON ERROR expression (EMPTY OBJECT) to the RETURNING type`, "", `expected JSON array`},
		} {
			t.Run(tc.expr+tc.doc+tc.path, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint, d.Detail = tc.hint, tc.detail
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+tc.expr+` AS v`, d, false, tc.doc, tc.path)
			})
		}
		for _, fn := range []string{"JSON_EXISTS", "JSON_QUERY", "JSON_VALUE"} {
			for _, clause := range []string{"", "ERROR ON ERROR"} {
				t.Run(fn+clause+" missing variable", func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42704", `could not find jsonpath variable "x"`)
					fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`($1::jsonb,$2::jsonpath `+clause+`) AS v`, d, false, `{}`, `$x`)
				})
			}
			t.Run(fn+" source document", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail, d.Context = `Token "x" is invalid.`, `JSON data, line 1: x`
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`($1::text::jsonb,'$') AS v`, d, false, `x`)
			})
			t.Run(fn+" source path", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of jsonpath input")
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`('{}',$1::text::jsonpath) AS v`, d, false, `$[`)
			})
			for _, tc := range []struct{ doc, path, code, message, detail, context, token string }{
				{`'x'`, `'$'`, "22P02", "invalid input syntax for type json", `Token "x" is invalid.`, `JSON data, line 1: x`, `'x'`},
				{`'{}'`, `'$['`, "42601", "syntax error at end of jsonpath input", "", "", `'$['`},
			} {
				t.Run(fn+" constant source "+tc.token, func(t *testing.T) {
					q := `/*é🙂*/ SELECT ` + fn + `(` + tc.doc + `,` + tc.path + `) AS v`
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail, d.Context = tc.detail, tc.context
					if tc.code == "42601" {
						d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
					}
					fixture.SQLJSONDiagnostic(t, p, b, c, q, d, tc.code == "42601")
				})
			}
			t.Run(fn+" unused passing failure", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
				q := `SELECT ` + fn + `('{}','$' PASSING $1::text::boolean AS unused) AS v`
				fixture.SQLJSONDiagnostic(t, p, b, c, q, d, false, "bad")
			})
			t.Run(fn+" constant preflight before NULL", func(t *testing.T) {
				// Bound parameters become constants during PostgreSQL preflight;
				// these errors do not witness runtime child evaluation.
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of jsonpath input")
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`(NULL::jsonb,$1::text::jsonpath PASSING $2::text::boolean AS unused) AS v`, d, false, "$[", "bad")
				d = fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`('{}',NULL::jsonpath PASSING $1::text::boolean AS unused) AS v`, d, false, "bad")
			})
			t.Run(fn+" skipped runtime casts", func(t *testing.T) {
				typ := "TEXT"
				if fn == "JSON_QUERY" {
					typ = "JSONB"
				}
				if fn == "JSON_EXISTS" {
					typ = "BOOL"
				}
				// Real table values cannot be folded into constant input casts.
				fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(`SELECT `+fn+`(doc,path::jsonpath PASSING binding::boolean AS unused) AS v FROM sqjq_runtime_inputs WHERE id=1`, typ, nil))
				fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(`SELECT `+fn+`(doc,path::jsonpath PASSING binding::boolean AS unused) AS v FROM sqjq_runtime_inputs WHERE id=2`, typ, nil))
				d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of jsonpath input")
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`(doc,path::jsonpath PASSING binding::boolean AS unused) AS v FROM sqjq_runtime_inputs WHERE id=3`, d, false)
				d = fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bad"`)
				fixture.SQLJSONDiagnostic(t, p, b, c, `SELECT `+fn+`(doc,path::jsonpath PASSING binding::boolean AS unused) AS v FROM sqjq_runtime_inputs WHERE id=4`, d, false)
			})
			// Native capability errors must escape selected error suppression. PG
			// executes this same demanded path and has its independent value.
			t.Run(fn+" demanded unsupported", func(t *testing.T) {
				q := `SELECT ` + fn + `($1::jsonb,$2::jsonpath) AS v`
				if !b.PG {
					d := fixture.ControlsReferenceDiagnostic("0A000", "stuntgres: JSONPath scalar methods are unsupported")
					fixture.SQLJSONDiagnostic(t, p, b, c, q, d, false, `2`, `$.double()`)
					return
				}
				typ, want := "TEXT", any("2")
				if fn == "JSON_QUERY" {
					typ = "JSONB"
				}
				if fn == "JSON_EXISTS" {
					typ, want = "BOOL", true
				}
				fixture.SQLJSONBoth(t, p, b, c, fixture.SQLJSONQueryOne(q, typ, want, `2`, `$.double()`))
			})
		}
		for _, prefix := range []string{"SELECT ", "/*é🙂*/ SELECT "} {
			for _, tc := range []struct{ expr, code, message, token, detail, hint string }{
				{`JSON_QUERY('{}','$' WITH WRAPPER OMIT QUOTES)`, "42601", `SQL/JSON QUOTES behavior must not be specified when WITH WRAPPER is used`, "JSON_QUERY", "", ""},
				{`JSON_VALUE('{}','$' RETURNING text FORMAT JSON)`, "42601", `cannot specify FORMAT JSON in RETURNING clause of JSON_VALUE()`, "FORMAT", "", ""},
				{`JSON_VALUE('{}',1)`, "42804", `JSON path expression must be of type jsonpath, not of type integer`, "1", "", ""},
				{`JSON_VALUE('{}',NULL::int)`, "42804", `JSON path expression must be of type jsonpath, not of type integer`, "NULL", "", ""},
				{`JSON_VALUE('{}','$' RETURNING SETOF text)`, "0A000", `returning SETOF types is not supported in SQL/JSON functions`, "", "", ""},
				{`JSON_QUERY('{}','$' RETURNING record)`, "0A000", `returning pseudo-types is not supported in SQL/JSON functions`, "", "", ""},
				{`JSON_EXISTS('{}','$' NULL ON ERROR)`, "42601", `invalid ON ERROR behavior`, "NULL", `Only ERROR, TRUE, FALSE, or UNKNOWN is allowed in ON ERROR for JSON_EXISTS().`, ""},
				{`JSON_VALUE('{}','$' EMPTY ARRAY ON ERROR)`, "42601", `invalid ON ERROR behavior`, "EMPTY", `Only ERROR, NULL, or DEFAULT expression is allowed in ON ERROR for JSON_VALUE().`, ""},
				{`JSON_QUERY('{}','$' TRUE ON ERROR)`, "42601", `invalid ON ERROR behavior`, "TRUE", `Only ERROR, NULL, EMPTY ARRAY, EMPTY OBJECT, or DEFAULT expression is allowed in ON ERROR for JSON_QUERY().`, ""},
				{`JSON_VALUE('{}','$' DEFAULT (SELECT 1) ON EMPTY)`, "42804", `can only specify a constant, non-aggregate function, or operator expression for DEFAULT`, "(SELECT", "", ""},
				{`JSON_VALUE('{}','$' DEFAULT count(*) ON EMPTY)`, "42804", `can only specify a constant, non-aggregate function, or operator expression for DEFAULT`, "count", "", ""},
				{`JSON_VALUE('{}','$' DEFAULT generate_series(1,2) ON EMPTY)`, "42804", `DEFAULT expression must not return a set`, "generate_series", "", ""},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail, d.Hint = tc.detail, tc.hint
					if tc.token != "" {
						d.Position = fixture.Position(len([]rune(prefix+tc.expr[:strings.Index(tc.expr, tc.token)])) + 1)
					}
					fixture.SQLJSONDiagnostic(t, p, b, c, prefix+tc.expr+` AS v`, d, true)
				})
			}
		}
		for _, tc := range []struct{ expr, token string }{
			{`JSON_EXISTS('{}','$' RETURNING boolean)`, "RETURNING"},
			{`JSON_VALUE('{}','$' WITH WRAPPER)`, "WITH"},
			{`JSON_VALUE('{}','$' OMIT QUOTES)`, "OMIT"},
			{`JSON_EXISTS('{}','$' NULL ON EMPTY)`, "EMPTY"},
			{`JSON_QUERY('{}','$') trailing garbage`, "garbage"},
		} {
			t.Run("syntax "+tc.expr, func(t *testing.T) {
				q := `/*é🙂*/ SELECT ` + tc.expr
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
				fixture.SQLJSONDiagnostic(t, p, b, c, q, d, true)
			})
		}
	})
}
