package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2ScalarBoundaries(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct {
			path    string
			s, n, b any
		}{
			{"'false'", "false", nil, true},
			{"false()", "false", float64(0), false},
			{"true()", "true", float64(1), true},
			{"0 div 0", "NaN", nil, false},
			{"0", "0", float64(0), false},
			{"16777217", "16777217", float64(16777216), true},
			{"number('not numeric')", "NaN", nil, false},
		} {
			xml2Both(t, p, b, c, xml2Scalars("<r/>", tc.path, tc.s, tc.n, tc.b))
		}
		for _, tc := range []struct {
			path, text string
			sign       int
		}{
			{"1 div 0", "Infinity", 1},
			{"-1 div 0", "-Infinity", -1},
			{"1000000000000000000000000000000000000000", "1e+39", 1},
		} {
			// Overflow's XPath string rendering is unrelated to FLOAT4 narrowing.
			q := "SELECT xpath_number('<r/>',$1::text) AS n"
			s := fixture.Select(q, []string{"n"}, []string{"FLOAT4"}, [][]any{{math.Inf(tc.sign)}}, tc.path)
			s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
				v, ok := value.(float64)
				if !ok || !math.IsInf(v, tc.sign) {
					t.Fatalf("infinite FLOAT4 raw value=%T(%v), sign=%d", value, value, tc.sign)
				}
				return true
			}
			xml2Both(t, p, b, c, s)
			if tc.path != "1000000000000000000000000000000000000000" {
				xml2Both(t, p, b, c, xml2Scalars("<r/>", tc.path, tc.text, math.Inf(tc.sign), true))
			}
		}
		for _, path := range []string{"", " ", "\t\n"} {
			xml2Both(t, p, b, c, fixture.Select("SELECT xpath_string($1::text,$2::text) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"ABC"}}, "<r>A<b>B</b>C</r>", path))
		}
		// A negative numeric value narrowed below FLOAT4's range retains its sign.
		s := fixture.Select("SELECT xpath_number('<r/>','-0.00000000000000000000000000000000000000000000000001') AS n", []string{"n"}, []string{"FLOAT4"}, [][]any{{math.Copysign(0, -1)}})
		s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
			v, ok := value.(float64)
			if !ok || v != 0 || !math.Signbit(v) {
				t.Fatalf("negative zero FLOAT4 raw=%T(%v)", value, value)
			}
			return true
		}
		xml2Both(t, p, b, c, s)
		s.Query = "SELECT xpath_number('<r/>','-0') AS n"
		xml2Both(t, p, b, c, s)
		xml2Both(t, p, b, c, fixture.Select("SELECT xpath_string('<r/>','1,2') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{nil}}))
		for _, fn := range []string{"xpath_number", "xpath_bool"} {
			xml2Failure(t, p, b, c, "SELECT "+fn+"('<r/>','1,2')", xml2Compile("Invalid expression"), false)
		}
	})
}
