package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONKeysValues(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			input string
			keys []string
		}{
			{`{}`, nil},
			{`{"f1":"abc","f2":{"f3":1,"f4":2}}`, []string{"f1", "f2"}},
			{`{"a":1,"a":2,"\u0061":3}`, []string{"a", "a", "a"}},
			{`{"":null}`, []string{""}},
			{`{"array":[{"ignored":1}],"object":{},"null":null}`, []string{"array", "object", "null"}},
			{`{"huge":1e1000000}`, []string{"huge"}},
			{`{"deep":` + strings.Repeat("[", 2000) + "0" + strings.Repeat("]", 2000) + "}", []string{"deep"}},
		} {
			t.Run(tc.input[:min(len(tc.input), 50)], func(t *testing.T) {
				for _, q := range []string{"SELECT json_object_keys($1::json)", "SELECT * FROM json_object_keys($1::json)"} {
					jsonKeysBoth(t, p, b, c, jsonKeysSelect(q, tc.keys, tc.input))
				}
				for _, q := range []string{"SELECT json_object_keys('" + tc.input + "'::json)", "SELECT * FROM json_object_keys('" + tc.input + "'::json)"} {
					jsonKeysBoth(t, p, b, c, jsonKeysSelect(q, tc.keys))
				}
			})
		}
		for _, q := range []string{"SELECT json_object_keys(NULL::json)", "SELECT * FROM json_object_keys(NULL::json)", "SELECT json_object_keys(NULL)", "SELECT json_object_keys($1)", "SELECT * FROM json_object_keys($1)"} {
			args := []any(nil)
			if strings.Contains(q, "$1") {
				args = []any{nil}
			}
			jsonKeysBoth(t, p, b, c, jsonKeysSelect(q, nil, args...))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN jk_json AS json", 0))
		for _, expr := range []string{`json_object_keys('{"a":1}')`, `pg_catalog.json_object_keys('{"a":1}')`, `"json_object_keys"('{"a":1}')`, `JSON_OBJECT_KEYS('{"a":1}')`, `json_object_keys('{"a":1}'::jk_json)`, `json_object_keys('{"a":1}'::text::json)`} {
			jsonKeysBoth(t, p, b, c, jsonKeysSelect("SELECT "+expr, []string{"a"}))
		}
		jsonKeysBoth(t, p, b, c, jsonKeysSelect("SELECT json_object_keys(NULL::jk_json)", nil))
	})
}

func TestReferenceJSONKeysUnicode(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		input := `{"":null,"a\"b":1,"x\\y":2,"\u00e9":3,"\uD83D\uDE04":4,"é":5}`
		for _, q := range []string{"SELECT json_object_keys($1)", "SELECT * FROM json_object_keys($1)"} {
			jsonKeysBoth(t, p, b, c, jsonKeysSelect(q, []string{"", `a"b`, `x\y`, "é", "😄", "é"}, input))
		}
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{`{"a":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`},
			{`{"a":{"\u0000":1}}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":{"\u0000...`},
			{`{"a":["\u0000"]}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":["\u0000...`},
			{`{"ok":1,"\u0000":2}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"ok":1,"\u0000...`},
			{`{"a":"\uD800"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\uD800"...`},
			{`{"a":{"\uDC00":1}}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":{"\uDC00...`},
			{`{"a":"\uD800\u0061"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\uD800\u0061...`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				jsonObjectBoth(t, p, b, c, fixture.Select("SELECT $1::json AS stored", []string{"stored"}, []string{"JSON"}, [][]any{{tc.input}}, tc.input))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				d.Context = tc.context
				for _, q := range []string{"SELECT json_object_keys($1)", "SELECT * FROM json_object_keys($1)", "SELECT json_object_keys('" + tc.input + "'::json)", "SELECT * FROM json_object_keys('" + tc.input + "'::json)"} {
					args := []any(nil)
					if strings.Contains(q, "$1") {
						args = []any{tc.input}
					}
					for _, prepared := range []bool{false, true} {
						jsonKeysError(t, p, b, c, q, prepared, d, "execute", args...)
					}
				}
			})
		}
	})
}
