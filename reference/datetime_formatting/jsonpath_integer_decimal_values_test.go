package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func integerDecimalInvalid(method, source string) string {
	typ := method
	if method == "decimal" {
		typ = "numeric"
	}
	return fmt.Sprintf(`argument "%s" of jsonpath item method .%s() is invalid for type %s`, source, method, typ)
}

func integerDecimalKind(method string) string {
	return "jsonpath item method ." + method + "() can only be applied to a string or numeric value"
}

func TestReferenceJSONPathIntegerDecimalValues(t *testing.T) {
	for _, method := range []string{"integer", "bigint"} {
		jsonpathNavigateCases(t, []jsonpathNavigateCase{
			{`[1.49,1.5,-1.49,-1.5,-0.0]`, `$[*].` + method + `()`, ``, `[1, 2, -1, -2, 0]`},
			{`["  +42 ","0x10","1_000","-0b10","0o17"]`, `$[*].` + method + `()`, ``, `[42, 16, 1000, -2, 15]`},
			{`[-1.5,2]`, `$[*].` + method + `().string()`, ``, `["-2", "2"]`},
			{`[-1.5,2]`, `$[*].` + method + `().type()`, ``, `["number", "number"]`},
		})
	}
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, method := range []string{"integer", "bigint"} {
			for _, source := range []string{"1.0", "1.5", "1e2", "", "1__0", "1_", "a\"b\\雪"} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array(to_jsonb($1::text),$2)`, "22036", integerDecimalInvalid(method, source), source, `$.`+method+`() `))
			}
		}
	})
}

func TestReferenceJSONPathIntegerDecimalBounds(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[2147483647,-2147483648,2147483647.49,-2147483648.49]`, `$[*].integer()`, ``, `[2147483647, -2147483648, 2147483647, -2147483648]`},
		{`[9223372036854775807,-9223372036854775808,9223372036854775807.49,-9223372036854775808.49,9007199254740993]`, `$[*].bigint()`, ``, `[9223372036854775807, -9223372036854775808, 9223372036854775807, -9223372036854775808, 9007199254740993]`},
		{`["9223372036854775807","-9223372036854775808"]`, `$[*].bigint()`, ``, `[9223372036854775807, -9223372036854775808]`},
		{`["2147483647","-2147483648"]`, `$[*].integer()`, ``, `[2147483647, -2147483648]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ method, number string }{
			{"integer", "2147483648"}, {"integer", "-2147483649"}, {"integer", "2147483647.5"}, {"integer", "-2147483648.5"},
			{"bigint", "9223372036854775808"}, {"bigint", "-9223372036854775809"}, {"bigint", "9223372036854775807.5"}, {"bigint", "-9223372036854775808.5"},
		} {
			for _, doc := range []string{tc.number, `"` + tc.number + `"`} {
				fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array($1,$2)`, "22036", integerDecimalInvalid(tc.method, tc.number), doc, `$.`+tc.method+`() `))
			}
		}
	})
}

func TestReferenceJSONPathIntegerDecimalDecimal(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[12345678901234567890.123400,"12345678901234567890.123400",1.230e2,-0.00]`, `$[*].decimal()`, ``, `[12345678901234567890.123400, 12345678901234567890.123400, 123.0, 0.00]`},
		{`[1.25,-1.25,99.94,-99.94]`, `$[*].decimal(3,1)`, ``, `[1.3, -1.3, 99.9, -99.9]`},
		{`[2.5,-2.5]`, `$[*].decimal(2)`, ``, `[3, -3]`},
		{`[1499,1500,-1500,99499,-99499]`, `$[*].decimal(2,-3)`, ``, `[1000, 2000, -2000, 99000, -99000]`},
		{`[0.009994,-0.009994,0.000004,-0.000004]`, `$[*].decimal(3,5)`, ``, `[0.00999, -0.00999, 0.00000, 0.00000]`},
		{`1`, `$.decimal(1)`, ``, `[1]`}, {`1`, `$.decimal(1000)`, ``, `[1]`},
		{`1`, `$.decimal(1,-1000)`, ``, `[0]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path string }{
			{`99.95`, `$.decimal(3,1)`}, {`-99.95`, `$.decimal(3,1)`}, {`99500`, `$.decimal(2,-3)`}, {`-99500`, `$.decimal(2,-3)`}, {`0.009995`, `$.decimal(3,5)`}, {`-0.009995`, `$.decimal(3,5)`},
		} {
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array($1,$2)`, "22036", integerDecimalInvalid("decimal", tc.doc), tc.doc, tc.path))
		}
		fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('0','$.decimal(1000,1000)') AS changed`, `[0.`+strings.Repeat("0", 1000)+`]`))
		for _, source := range []string{"1e131072", "1e-16384"} {
			fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array(to_jsonb($1::text),'$.decimal(1,-1000)')`, "22036", integerDecimalInvalid("decimal", source), source))
		}
	})
}
