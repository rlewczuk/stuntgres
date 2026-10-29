package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func numericMethodsInvalid(method, source string) string {
	typ := "numeric"
	if method == "double" { typ = "double precision" }
	return `argument "` + source + `" of jsonpath item method .` + method + `() is invalid for type ` + typ
}

func numericMethodsKind(method string) string {
	kind := "numeric"
	if method == "number" || method == "double" { kind = "string or numeric" }
	return "jsonpath item method ." + method + "() can only be applied to a " + kind + " value"
}

func numericMethodsBad(method string) string {
	if method == "number" || method == "double" { return numericMethodsInvalid(method, "bad") }
	return numericMethodsKind(method)
}

func TestReferenceJSONPathNumericMethodsValues(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[0,1,-2,-3.4,5.6]`, `$[*].abs()`, ``, `[0, 1, 2, 3.4, 5.6]`},
		{`[0,1,-2,-3.4,5.6]`, `$[*].floor()`, ``, `[0, 1, -2, -4, 5]`},
		{`[0,1,-2,-3.4,5.6]`, `$[*].ceiling()`, ``, `[0, 1, -2, -3, 6]`},
		{`[-0.00,-1.2300,1.2300]`, `$[*].abs()`, ``, `[0.00, 1.2300, 1.2300]`},
		{`[-0.00,-1.2300,1.2300]`, `$[*].floor()`, ``, `[0, -2, 1]`},
		{`[-0.00,-1.2300,1.2300]`, `$[*].ceiling()`, ``, `[0, -1, 2]`},
		{`[-0.0001,0.0001,1.000]`, `$[*].floor()`, ``, `[-1, 0, 1]`},
		{`[-0.0001,0.0001,1.000]`, `$[*].ceiling()`, ``, `[0, 1, 1]`},
		{`[-12345678901234567890.123400,9007199254740993]`, `$[*].abs()`, ``, `[12345678901234567890.123400, 9007199254740993]`},
		{`[-12345678901234567890.123400,9007199254740993]`, `$[*].floor()`, ``, `[-12345678901234567891, 9007199254740993]`},
		{`[-12345678901234567890.123400,9007199254740993]`, `$[*].ceiling()`, ``, `[-12345678901234567890, 9007199254740993]`},
		{`[-1.2300,2]`, `$[*].abs().string()`, ``, `["1.2300", "2"]`},
		{`[-1.2,2]`, `$[*].ceiling().type()`, ``, `["number", "number"]`},
	})
}

func TestReferenceJSONPathNumericMethodsConversions(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`, `$[*].number()`, ``, `[9007199254740993, 9007199254740993, 1.2300, 1.2300]`},
		{`[9007199254740993,"9007199254740993",1.2300,"1.2300"]`, `$[*].double()`, ``, `[9007199254740993, 9007199254740990, 1.2300, 1.23]`},
		{`["1.234567890123456789",-3.4,"-3.4",-0.00,"-0.00"]`, `$[*].double()`, ``, `[1.23456789012346, -3.4, -3.4, 0.00, 0]`},
		{`"1.234567890123456789"`, `$.number()`, ``, `[1.234567890123456789]`},
		{`1e1000`, `$.number()`, ``, `[` + "1" + strings.Repeat("0",1000) + `]`},
		{`1e-400`, `$.number()`, ``, `[0.` + strings.Repeat("0",399) + `1]`},
		{`1e-320`, `$.double()`, ``, `[0.` + strings.Repeat("0",319) + `1]`},
		{`"1e-320"`, `$.double()`, ``, `[0.` + strings.Repeat("0",320) + `999988867182683]`},
		{`"1.7976931348623157e308"`, `$.double()`, ``, `[` + "179769313486232" + strings.Repeat("0",294) + `]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _, source := range []string{"1e1000","1e-400","1.7976931348623159e308","2e-324"} {
			for _, doc := range []string{source,`"`+source+`"`} {
				canonical := source
				if doc == source {
					switch source {
					case "1e1000": canonical = "1"+strings.Repeat("0",1000)
					case "1e-400": canonical = "0."+strings.Repeat("0",399)+"1"
					case "1.7976931348623159e308": canonical = "17976931348623159"+strings.Repeat("0",292)
					case "2e-324": canonical = "0."+strings.Repeat("0",323)+"2"
					}
				}
				fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array($1,'$.double()')`,"22036",numericMethodsInvalid("double",canonical),doc))
			}
		}
	})
}

func TestReferenceJSONPathNumericMethodsEncoded(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`["  +42 ","0x10","0o10","0b10","1_000","\u0031\u002e\u0032\u0035"]`, `$[*].number()`, ``, `[42, 16, 8, 2, 1000, 1.25]`},
		{`["0x10","0X10","0x10  ","0x1.8p1","-0x1.8p1","0x.8","+0X1P+4","0x0p-9999","-0x0"]`, `$[*].double()`, ``, `[16, 16, 16, 3, -3, 0.5, 16, 0, 0]`},
		{`"0x1p-1074"`, `$.double()`, ``, `[0.` + strings.Repeat("0",323) + `494065645841247]`},
	})
	jsonpathNavigateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		for _, source := range []string{"1_000","0x","0x.p1","0x1p","0x1p+","0x1_0","0x1p2junk","0x1p1024","0x1p-1075","NaN(x-y)","NaN(foo)junk","a\"b\\雪"} {
			fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array(to_jsonb($1::text),'$.double()')`,"22036",numericMethodsInvalid("double",source),source))
		}
		for _, source := range []string{"NaN(payload)","NaN()","+NaN","nan(foo_bar)","-NaN(foo)","Inf","+Infinity","-Infinity"} {
			fixture.JSONPathNavigateBoth(t,p,b,c,jsonpathNavigateError(`SELECT jsonb_path_query_array(to_jsonb($1::text),'$.double()')`,"22036",`NaN or Infinity is not allowed for jsonpath item method .double()`,source))
		}
	})
}
