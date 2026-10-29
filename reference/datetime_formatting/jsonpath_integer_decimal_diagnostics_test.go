package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathIntegerDecimalGrammar(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`1234.5678`, `$.decimal(+6,-2)`, ``, `[1200]`},
		{`1`, `$.decimal()`, ``, `[1]`}, {`1`, `$.decimal(2)`, ``, `[1]`},
		{`[]`, `$.decimal(0)`, ``, `[]`}, {`{}`, `$.absent.decimal(0)`, ``, `[]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ path, token, detail string }{
			{`$.integer(1)`, "1", ""}, {`$.bigint(1)`, "1", ""},
			{`$.decimal(1.0)`, "1.0", ""}, {`$.decimal(1e0)`, "1e0", ""},
			{`$.decimal($p)`, "$p", ""}, {`$.decimal(1+1)`, "+", ""},
			{`$.decimal(1,2,3)`, "", `.decimal() can only have an optional precision[,scale].`},
			{`$.integer() junk`, "junk", ""},
		} {
			message := `syntax error at or near "` + tc.token + `" of jsonpath input`
			if tc.detail != "" {
				message = `invalid input syntax for type jsonpath`
			}
			if tc.path == `$.integer() junk` {
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

func TestReferenceJSONPathIntegerDecimalDiagnostics(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct{ doc, path, code, message string }{
			{`1`, `$.decimal(0)`, "22023", `NUMERIC precision 0 must be between 1 and 1000`},
			{`1`, `$.decimal(1001)`, "22023", `NUMERIC precision 1001 must be between 1 and 1000`},
			{`1`, `$.decimal(1,1001)`, "22023", `NUMERIC scale 1001 must be between -1000 and 1000`},
			{`1`, `$.decimal(1,-1001)`, "22023", `NUMERIC scale -1001 must be between -1000 and 1000`},
			{`1`, `$.decimal(2147483648)`, "22036", `precision of jsonpath item method .decimal() is out of range for type integer`},
			{`1`, `$.decimal(-2147483649)`, "22036", `precision of jsonpath item method .decimal() is out of range for type integer`},
			{`1`, `$.decimal(0,2147483648)`, "22036", `scale of jsonpath item method .decimal() is out of range for type integer`},
			{`1`, `$.decimal(0,1001)`, "22023", `NUMERIC precision 0 must be between 1 and 1000`},
			{`1`, `$.decimal(1,-2147483649)`, "22036", `scale of jsonpath item method .decimal() is out of range for type integer`},
			{`1`, `$.decimal(-1)`, "22023", `NUMERIC precision -1 must be between 1 and 1000`},
			{`null`, `$.decimal(0)`, "22036", integerDecimalKind("decimal")},
			{`"bad"`, `$.decimal(0)`, "22036", integerDecimalInvalid("decimal", "bad")},
			{`"NaN"`, `$.decimal(0)`, "22036", `NaN or Infinity is not allowed for jsonpath item method .decimal()`},
			{`"Infinity"`, `$.decimal()`, "22036", `NaN or Infinity is not allowed for jsonpath item method .decimal()`},
			{`"-Infinity"`, `$.decimal()`, "22036", `NaN or Infinity is not allowed for jsonpath item method .decimal()`},
			{`" 1.5 "`, `$.integer()`, "22036", integerDecimalInvalid("integer", " 1.5 ")},
			{`"a\"b\\雪"`, `$.decimal()`, "22036", integerDecimalInvalid("decimal", `a"b\雪`)},
			{`1.23400e3`, `$.decimal(1)`, "22036", integerDecimalInvalid("decimal", "1234.00")},
		}
		for _, method := range []string{"integer", "bigint", "decimal"} {
			for _, doc := range []string{`null`, `true`, `{}`, `[]`, `[1]`} {
				cases = append(cases, struct{ doc, path, code, message string }{doc, `strict $.` + method + `()`, "22036", integerDecimalKind(method)})
			}
			cases = append(cases, struct{ doc, path, code, message string }{`"2023-08-15"`, `$.datetime().` + method + `()`, "22036", integerDecimalKind(method)})
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
