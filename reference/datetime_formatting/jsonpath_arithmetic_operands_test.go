package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathArithmeticOperands(t *testing.T) {
	jsonpathArithmeticCases(t, []jsonpathNavigateCase{
		{jsonpathArithmeticDocument, `$.one + 3`, "", `[5]`},
		{jsonpathArithmeticDocument, `strict $.one[0] + 3`, "", `[5]`},
		{jsonpathArithmeticDocument, `+$.missing`, "", `[]`},
		{jsonpathArithmeticDocument, `strict -$.seq[*]`, "", `[-2, -3, -4]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, operand := range []string{`$.seq`, `$.nested`, `$.missing`, `"2"`, `null`, `true`, `$obj`, `strict $.one`} {
			for _, side := range []string{"left", "right"} {
				path := operand + ` + 1`
				if side == "right" {
					path = `1 + ` + operand
				}
				// Mode is a path prefix, not an operand expression.
				if operand == `strict $.one` {
					path = `strict $.one + 1`
					if side == "right" {
						path = `strict 1 + $.one`
					}
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, "22038", side+` operand of jsonpath operator + is not a single numeric value`, jsonpathArithmeticDocument, path, `{"obj":{}}`, false))
			}
		}
		for _, path := range []string{`strict +$.seq`, `+$.nested`, `-"2"`, `+null`, `-true`, `+$obj`} {
			op := "+"
			if path[0] == '-' {
				op = "-"
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, "2203B", `operand of unary jsonpath operator `+op+` is not a numeric value`, jsonpathArithmeticDocument, path, `{"obj":{}}`, false))
		}
	})
}

func TestReferenceJSONPathArithmeticDemand(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, code, message string }{
			{`[1,2]`, `$[*] + $v`, "42704", `could not find jsonpath variable "v"`},
			{`[1,2]`, `$[*] + (1 / 0)`, "22012", `division by zero`},
			{`[1,2]`, `(1 / 0) + $v`, "22012", `division by zero`},
			{`[1,2]`, `$[*] + "x"`, "22038", `left operand of jsonpath operator + is not a single numeric value`},
			{`[1]`, `+ $[0,$v]`, "42704", `could not find jsonpath variable "v"`},
			{`[1]`, `strict + $[0,2]`, "22033", `jsonpath array subscript is out of bounds`},
			{`[1,2]`, `strict $[*] + $.x`, "2203A", `jsonpath member accessor can only be applied to an object`},
			{`[1]`, `strict ($[0] + 1)[0]`, "22039", `jsonpath array accessor can only be applied to an array`},
			{`[1]`, `strict (-$[*])[0]`, "22039", `jsonpath array accessor can only be applied to an array`},
		} {
			for _, silent := range []bool{false, true} {
				if silent && tc.code != "42704" {
					continue
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(jsonpathArithmeticSQL, tc.code, tc.message, tc.doc, tc.path, `{}`, silent))
			}
		}
		for _, tc := range []struct{ doc, path, want string }{
			{`{}`, `$.missing?(1 / 0 > 0)`, `[]`},
			{`1`, `(1 == 1) || (1 / 0 > 0)`, `[true]`},
			{`1`, `(1 == 2) && (1 / 0 > 0)`, `[false]`},
			{`[1,"2",3]`, `+$`, `[1]`},
			{`[1]`, `strict + $[0,2]`, `[]`},
			{`[1,2,3]`, `(-$[*])?(@ < -1)`, `[-2, -3]`},
			{`[1]`, `strict ($[0] + 1)[0]`, `[]`},
			{`[1]`, `strict (-$[*])[0]`, `[]`},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(jsonpathArithmeticSQL, tc.want, tc.doc, tc.path, `{}`, true))
		}
	})
}
