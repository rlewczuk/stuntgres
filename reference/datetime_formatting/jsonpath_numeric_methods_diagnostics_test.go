package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNumericMethodsGrammar(t *testing.T) {
	for _, method := range []string{"abs", "floor", "ceiling", "number", "double"} {
		jsonpathNavigateCases(t, []jsonpathNavigateCase{
			{`1`, `$.`+method+`().type()`, ``, `["number"]`},
			{`{"`+method+`":42}`, `$.`+method, ``, `[42]`},
			{`{}`, `$.missing.`+method+`()`, ``, `[]`},
		})
	}
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, method := range []string{"abs", "floor", "ceiling", "number", "double"} {
			for _, prepared := range []bool{false, true} {
				jsonpathReferenceOutput(t,p,b,` lax $ [*] . `+method+` ( ) `,`$[*].`+method+`()`,prepared)
			}
		}
		for _, tc := range []struct{ path, token, detail string }{
			{`$.abs(1)`, "1", ""}, {`$.floor(1)`, "1", ""}, {`$.ceiling(1)`, "1", ""},
			{`$.number(1)`, "1", ""}, {`$.double(1)`, "1", ""},
			{`$.double() junk`, "junk", ""},
		} {
			message := `syntax error at or near "` + tc.token + `" of jsonpath input`
			if tc.detail != "" {
				message = `invalid input syntax for type jsonpath`
			}
			if tc.path == `$.double() junk` {
				message = `syntax error at end of jsonpath input`
			}
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				literal := fixture.JSONReferenceQuote(tc.path)
				q := prefix + `jsonb_path_query_array(NULL::jsonb,` + literal + `::jsonpath,silent=>true)`
				d := fixture.ControlsReferenceDiagnostic("42601", message)
				d.Detail = tc.detail
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, literal)])) + 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			d := fixture.ControlsReferenceDiagnostic("42601", message)
			d.Detail = tc.detail
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array($1,$2)`, d, `1`, tc.path)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceJSONPathNumericMethodsDiagnostics(t *testing.T) {
	t.Run("arithmetic_overflow",func(t *testing.T) {
		jsonpathNavigateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			for _, method := range []string{"floor","ceiling"} {
				doc:=strings.Repeat("9",131072)+".1"
				if method=="floor" {doc="-"+doc}
				for _, silent:=range []bool{false,true} {
					fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array($1,$2,'{}',$3)`,"22003","value overflows numeric format",doc,"$."+method+"()",silent))
				}
			}
		})
	})
	t.Run("json_input_precedes_silent",func(t *testing.T) {
		jsonpathNavigateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			for _, token:=range []string{"NaN","Infinity"} {
				d:=fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type json`)
				d.Detail=`Token "`+token+`" is invalid.`
				d.Context="JSON data, line 1: "+token+"\nunnamed portal parameter $1 = '...'"
				s:=fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array($1,'$.number()',silent=>true)`,d,token)
				s.SelectRows=true
				s.ErrorStage="execute"
				fixture.JSONPathNavigateBoth(t,p,b,c,s)
			}
		})
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct{ doc, path, code, message string }{
			{`"1e131072"`, `$.number()`, "22036", numericMethodsInvalid("number","1e131072")},
			{`"1e-16384"`, `$.number()`, "22036", numericMethodsInvalid("number","1e-16384")},
			{`"  a\"b\\雪  "`, `$.number()`, "22036", numericMethodsInvalid("number",`  a"b\雪  `)},
			{`"bad"`, `$.number()`, "22036", numericMethodsInvalid("number", "bad")},
			{`"a\"b\\雪"`, `$.double()`, "22036", numericMethodsInvalid("double", `a"b\雪`)},
			{`"NaN"`, `$.number()`, "22036", `NaN or Infinity is not allowed for jsonpath item method .number()`},
			{`"Infinity"`, `$.number()`, "22036", `NaN or Infinity is not allowed for jsonpath item method .number()`},
			{`"-Infinity"`, `$.number()`, "22036", `NaN or Infinity is not allowed for jsonpath item method .number()`},
		}
		for _, method := range []string{"abs", "floor", "ceiling", "number", "double"} {
			for _, doc := range []string{`null`, `true`, `{}`, `[]`, `[1]`} {
				cases = append(cases, struct{ doc, path, code, message string }{doc, `strict $.` + method + `()`, "22036", numericMethodsKind(method)})
			}
			cases = append(cases, struct{ doc, path, code, message string }{`"2023-08-15"`, `$.datetime().` + method + `()`, "22036", numericMethodsKind(method)})
		}
		for _, tc := range cases {
			q := `SELECT jsonb_path_query_array($1,$2,'{}',$3)`
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, tc.code, tc.message, tc.doc, tc.path, false))
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(q+` AS changed`, `[]`, tc.doc, tc.path, true))
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				literal := prefix + `jsonb_path_query_array(` + fixture.JSONReferenceQuote(tc.doc) + `,` + fixture.JSONReferenceQuote(tc.path) + `)`
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(literal, tc.code, tc.message))
			}
		}
	})
}
