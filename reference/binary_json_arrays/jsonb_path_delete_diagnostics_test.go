package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPathDeleteDiagnostics(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT $1::jsonb #- $2::text[] AS v"
		for _, tc := range []struct{ doc, path, code, msg string }{
			{`{}`, `{{a,b}}`, "2202E", "wrong number of array subscripts"},
			{`1`, `{{a,b}}`, "2202E", "wrong number of array subscripts"},
			{`1`, `{}`, "22023", "cannot delete path in scalar"},
			{`"s"`, `{a}`, "22023", "cannot delete path in scalar"},
			{`true`, `{a}`, "22023", "cannot delete path in scalar"},
			{`null`, `{}`, "22023", "cannot delete path in scalar"},
			{`{"a":1}`, `{a,NULL}`, "22004", "path element at position 2 is null"},
			{`{"a":1}`, `[-2:-1]={a,NULL}`, "22004", "path element at position 2 is null"},
			{`[1]`, `{x}`, "22P02", `path element at position 1 is not an integer: "x"`},
			{`{"a":[]}`, `{a,bad}`, "22P02", `path element at position 2 is not an integer: "bad"`},
		} {
			t.Run(tc.doc+tc.path, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteError(q, d, tc.doc, tc.path))
				literal := "SELECT " + jsonTypeQuote(tc.doc) + "::jsonb #- " + jsonTypeQuote(tc.path) + "::text[] AS v"
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteError(literal, d))
			})
		}
		for _, index := range []string{"", "1 ", "1x", "2147483648", "-2147483649"} {
			d := fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "`+index+`"`)
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteError("SELECT $1::jsonb #- ARRAY[$2::text] AS v", d, "[1,2]", index))
		}
		for _, tc := range []struct {
			q, sig string
			pos    int
		}{
			{`SELECT '{}'::jsonb #- 'x'::text`, "jsonb #- text", 20},
			{`SELECT /*é*/ '{}'::jsonb #- 'x'::text`, "jsonb #- text", 26},
			{`SELECT '{}'::json #- '{a}'::text[]`, "json #- text[]", 19},
			{`SELECT '{}'::jsonb #- ARRAY[1]`, "jsonb #- integer[]", 20},
			{`SELECT NULL::jsonb #- 'x'::text WHERE FALSE`, "jsonb #- text", 20},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.sig)
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(tc.pos)
			jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError(tc.q, d))
		}
		for _, left := range []string{"'{}'::jsonb", "NULL::jsonb"} {
			literal := "SELECT " + left + " #- '{a,,b}'::text[]"
			d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
			d.Detail = `Unexpected "," character.`
			d.Position = fixture.Position(len([]rune("SELECT "+left+" #- ")) + 1)
			jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError(literal, d))
		}
		for _, left := range []any{`{}`, nil} {
			d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
			d.Detail, d.Context = `Unexpected "," character.`, "unnamed portal parameter $2 = '...'"
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteError(q, d, left, "{a,,b}"))
		}
		for _, path := range []any{"{a}", nil} {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail, d.Context = `Token "TRUE" is invalid.`, "JSON data, line 1: TRUE\nunnamed portal parameter $1 = '...'"
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteError(q, d, "TRUE", path))
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbd_empty_input(doc jsonb)", 0))
		d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: jsonb #- text")
		d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "12"
		jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError("SELECT doc #- 'x'::text FROM jbd_empty_input", d))
		d = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
		d.Position = "41"
		jsonTypeAnalysisError(t, p, b, c, jsonbPathDeleteError(`SELECT '{}'::jsonb #- '{}'::text[] AS v garbage`, d))
	})
}
