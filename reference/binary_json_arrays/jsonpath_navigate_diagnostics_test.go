package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateDiagnostics(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb,false) AS changed`
		for _, tc := range []struct{ doc, path, vars, code, message string }{
			{`{}`, `strict $.x`, `{}`, "2203A", `JSON object does not contain key "x"`},
			{`1`, `strict $.x`, `{}`, "2203A", `jsonpath member accessor can only be applied to an object`},
			{`1`, `strict $.*`, `{}`, "2203C", `jsonpath wildcard member accessor can only be applied to an object`},
			{`1`, `strict $[0]`, `{}`, "22039", `jsonpath array accessor can only be applied to an array`},
			{`1`, `strict $[*]`, `{}`, "22039", `jsonpath wildcard array accessor can only be applied to an array`},
			{`[1]`, `strict $[-1]`, `{}`, "22033", `jsonpath array subscript is out of bounds`},
			{`[1]`, `strict $[1]`, `{}`, "22033", `jsonpath array subscript is out of bounds`},
			{`[1,2,3]`, `strict $[2 to 0]`, `{}`, "22033", `jsonpath array subscript is out of bounds`},
			{`[1]`, `$[2147483648]`, `{}`, "22033", `jsonpath array subscript is out of integer range`},
			{`[1]`, `$[$i]`, `{"i":"x"}`, "22033", `jsonpath array subscript is not a single numeric value`},
			{`[1]`, `$[$i]`, `{"i":[0,1]}`, "22033", `jsonpath array subscript is not a single numeric value`},
			{`1`, `strict (1 == 1)[0]`, `{}`, "22039", `jsonpath array accessor can only be applied to an array`},
			{`1`, `$v`, `{}`, "42704", `could not find jsonpath variable "v"`},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, tc.code, tc.message, tc.doc, tc.path, tc.vars))
		}
		for _, tc := range []struct{ path, code, message, detail string }{
			{`$[`, "42601", `syntax error at end of jsonpath input`, ""},
			{`$[]`, "42601", `syntax error at or near "]" of jsonpath input`, ""},
			{`@`, "42601", `@ is not allowed in root expressions`, ""},
			{`$ like_regex "("`, "2201B", `invalid regular expression: parentheses () not balanced`, ""},
			{`$ like_regex "a" flag "a"`, "42601", `invalid input syntax for type jsonpath`, `Unrecognized flag character "a" in LIKE_REGEX predicate.`},
			{`$ like_regex "a" flag "x"`, "0A000", `XQuery "x" flag (expanded regular expressions) is not implemented`, ""},
		} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				literal := jsonReferenceQuote(tc.path)
				sql := prefix + `jsonb_path_query_array(NULL::jsonb,` + literal + `::jsonpath,silent => true)`
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail = tc.detail
				d.Position = fixture.Position(len([]rune(sql[:strings.Index(sql, literal)])) + 1)
				s := fixture.ErrorDiagnostic(sql, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Detail = tc.detail
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(q, d, `1`, tc.path, `{}`)
			s.SelectRows = true
			s.ErrorStage = "execute"
			jsonpathNavigateBoth(t, p, b, c, s)
		}
		bound := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		for _, slot := range []int{0, 2, 3} {
			args := []any{`1`, `$`, `{}`, false}
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type json`)
			d.Detail = `Token "0x10" is invalid.`
			parameter := map[int]string{0: "1", 2: "3", 3: "4"}[slot]
			d.Context = "JSON data, line 1: 0x10\nunnamed portal parameter $" + parameter + " = '...'"
			args[slot] = `0x10`
			if slot == 3 {
				args[slot] = "bad"
				d.Message = `invalid input syntax for type boolean: "bad"`
				d.Detail = ""
				d.Context = "unnamed portal parameter $4 = '...'"
			}
			s := fixture.ErrorDiagnostic(bound, d, args...)
			s.SelectRows = true
			s.ErrorStage = "execute"
			jsonpathNavigateBoth(t, p, b, c, s)
		}
	})
}
