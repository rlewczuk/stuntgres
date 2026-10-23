package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathArithmeticDiagnostics(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, op := range []string{"+", "-", "*", "/", "%"} {
			for _, side := range []string{"left", "right"} {
				path := `"x" ` + op + ` 1`
				if side == "right" {
					path = `1 ` + op + ` "x"`
				}
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, "22038", side+` operand of jsonpath operator `+op+` is not a single numeric value`, `1`, path, `{}`, false))
			}
		}
		for _, op := range []string{"+", "-"} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, "2203B", `operand of unary jsonpath operator `+op+` is not a numeric value`, `"x"`, op+`$`, `{}`, false))
		}
		for _, path := range []string{`1 / 0`, `1 / -0.00`, `1 % 0`, `1 % -0.00`, `1e131071 * 10`} {
			code, message := "22012", "division by zero"
			if strings.Contains(path, "131071") {
				code, message = "22003", "value overflows numeric format"
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, code, message, `1`, path, `{}`, false))
			// Runtime diagnostics should not gain a fabricated SQL position, even
			// with a multibyte prefix; the oracle gate resolves optional fields.
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				q := prefix + `jsonb_path_query_array('1',` + jsonReferenceQuote(path) + `) AS changed`
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(q, code, message))
			}
		}
		for _, tc := range []struct{ path, message string }{
			{`1 +`, `syntax error at end of jsonpath input`},
			{`@ + 1`, `@ is not allowed in root expressions`},
			{`last + 1`, `LAST is allowed only in array subscripts`},
		} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				literal := jsonReferenceQuote(tc.path)
				q := prefix + `jsonb_path_query_array('1',` + literal + `::jsonpath) AS changed`
				d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, literal)])) + 1)
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(jsonpathArithmeticSQL, d, `1`, tc.path, `{}`, false)
			s.SelectRows, s.ErrorStage = true, "execute"
			jsonpathNavigateBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ call, signature string }{
			{`jsonb_path_query_array('1','$ + 1'::text)`, `jsonb_path_query_array(unknown, text)`},
			{`jsonb_path_query_array('1','$ + 1','{}',false,false)`, `jsonb_path_query_array(unknown, unknown, unknown, boolean, boolean)`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			s := fixture.ErrorDiagnostic("SELECT "+tc.call, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceJSONPathArithmeticSuppression(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, path := range []string{`"x" + 1`, `strict "x" + 1`, `1 / 0`, `1 % 0`, `1e131071 * 10`} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathArithmeticSQL, `[]`, `1`, path, `{}`, true))
		}
		for _, silent := range []bool{false, true} {
			for _, tc := range []struct{ path, want string }{
				{`$[*]?(2 / @ > 0)`, `[1, 2, 3]`},
				{`$[*]?((2 / @ > 0) is unknown)`, `[0]`},
				{`("x" + 1) == 1`, `[null]`},
			} {
				jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathArithmeticSQL, tc.want, `[1,2,0,3]`, tc.path, `{}`, silent))
			}
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, "42704", `could not find jsonpath variable "v"`, `1`, `($v + 1) > 0`, `{}`, silent))
		}
	})
}
