package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceJSONPathArithmeticValues(t *testing.T) {
	var cases []jsonpathNavigateCase
	for _, tc := range []struct{ path, want string }{
		{`$.a + $.b`, `[3.500]`}, {`$.a - $.a`, `[0.00]`},
		{`$.a * $.b`, `[2.76000]`}, {`$.neg / 2`, `[-3.5000000000000000]`},
		{`$.neg % 2`, `[-1.00]`}, {`$.big + 1`, `[9007199254740994]`},
		{`+$.seq`, `[2, 3, 4]`}, {`-$.seq`, `[-2, -3, -4]`},
		{`-(-$.a)`, `[1.20]`}, {`-($.a - $.a)`, `[0.00]`},
	} {
		cases = append(cases, jsonpathNavigateCase{jsonpathArithmeticDocument, tc.path, "", tc.want})
	}
	jsonpathArithmeticCases(t, cases)
}

func TestReferenceJSONPathArithmeticNumericBoundaries(t *testing.T) {
	jsonpathArithmeticCases(t, []jsonpathNavigateCase{
		{`1`, `1 / 3`, "", `[0.33333333333333333333]`},
		{`1`, `2 / 3`, "", `[0.66666666666666666667]`},
		{`1`, `1 / 1`, "", `[1.00000000000000000000]`},
		{`1`, `7 % -2`, "", `[1]`}, {`1`, `-7 % -2`, "", `[-1]`},
		{`1`, `-4.00 % 2`, "", `[0.00]`},
		{`1`, `99999999999999999999 + 1`, "", `[100000000000000000000]`},
		{`1`, `1e-1000 / 1`, "", `[0.` + strings.Repeat("0", 999) + `1]`},
		// Half away from zero at the representation's fractional boundary.
		{`1`, `5e-8192 * 1e-8192`, "", `[0.` + strings.Repeat("0", 16382) + `1]`},
		{`1`, `-5e-8192 * 1e-8192`, "", `[-0.` + strings.Repeat("0", 16382) + `1]`},
		{`1`, `4e-8192 * 1e-8192`, "", `[0.` + strings.Repeat("0", 16383) + `]`},
	})
}

func TestReferenceJSONPathArithmeticGrammarBinding(t *testing.T) {
	jsonpathArithmeticCases(t, []jsonpathNavigateCase{
		{`1`, `1 + 2 * 3`, "", `[7]`}, {`1`, `(1 + 2) * 3`, "", `[9]`},
		{`1`, `8 / 2 / 2`, "", `[2.0000000000000000]`},
		{`[10,20,30]`, `$[last - 1]`, "", `[20]`},
		{`[10,20,30]`, `$[last - (1 + 0)]`, "", `[20]`},
		{`[1,2,3]`, `$[*]?(@ * $n > 4)`, `{"n":2}`, `[3]`},
		{`{"n":2,"a":[1,2,3]}`, `$.a[*]?(@ + $.n == 4)`, "", `[2]`},
		{`{"a":[2,3]}`, `-(-$.a)`, "", `[2, 3]`},
	})
}
