package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathMethodsDiagnostics(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, code, message string }{
			{`1`, `strict $.size()`, "22039", jsonpathMethodsSizeError},
			{`null`, `$.boolean()`, "22036", jsonpathMethodsBooleanError},
			{`{}`, `$.string()`, "22036", jsonpathMethodsStringError},
			{`1`, `$.keyvalue()`, "2203C", jsonpathMethodsKeyValueError},
		} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				q := prefix + `jsonb_path_query_array(` + jsonReferenceQuote(tc.doc) + `,` + jsonReferenceQuote(tc.path) + `) AS changed`
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, tc.code, tc.message))
			}
		}
		for _, path := range []string{`$.type(1)`, `$.size(1)`, `$.boolean(1)`, `$.string(1)`, `$.keyvalue(1)`, `1.type()`} {
			message := `syntax error at or near "1" of jsonpath input`
			if path == `1.type()` {
				message = `trailing junk after numeric literal at or near "1.t" of jsonpath input`
			}
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				literal := jsonReferenceQuote(path)
				q := prefix + `jsonb_path_query_array(NULL::jsonb,` + literal + `::jsonpath,silent=>true)`
				d := fixture.ControlsReferenceDiagnostic("42601", message)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, literal)])) + 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			d := fixture.ControlsReferenceDiagnostic("42601", message)
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array($1,$2)`, d, `1`, path)
			s.SelectRows = true
			s.ErrorStage = "execute"
			jsonpathNavigateBoth(t, p, b, c, s)
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('0','(1).type()') AS changed`, `["number"]`))
		for _, tc := range []struct{ q, message, token string }{
			{`SELECT jsonb_path_query_array(1,'$.type()')`, `function jsonb_path_query_array(integer, unknown) does not exist`, `jsonb_path_query_array`},
			{`SELECT jsonb_path_query_array('1','$.type()','{}',false,true)`, `function jsonb_path_query_array(unknown, unknown, unknown, boolean, boolean) does not exist`, `jsonb_path_query_array`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
			d.Position = fixture.Position(strings.Index(tc.q, tc.token) + 1)
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
