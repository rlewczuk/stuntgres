package sqltype

import "testing"

func TestMathScalarExpDecimalRounding(t *testing.T) {
	for _, tc := range []struct {
		coefficient     string
		exponent, scale int64
		want            string
		wantExponent    int64
	}{
		{"125", -2, 1, "13", -1}, {"-125", -2, 1, "-13", -1},
		{"124", -2, 1, "12", -1}, {"-124", -2, 1, "-12", -1},
		{"9995", -3, 2, "1000", -2}, {"-9995", -3, 2, "-1000", -2},
		{"1", -20, 16, "0", -16}, {"-1", -20, 16, "0", -16},
		{"123", 0, 16, "123", 0},
	} {
		var w expScratch
		var value expDecimal
		value.coefficient.SetString(tc.coefficient, 10)
		value.exponent = tc.exponent
		w.round(&value, &value.coefficient, value.exponent, tc.scale)
		if value.coefficient.String() != tc.want || value.exponent != tc.wantExponent {
			t.Fatal(tc, value.coefficient.String(), value.exponent)
		}
	}
	var w expScratch
	for _, tc := range []struct {
		input string
		want  int64
	}{{"0", 1}, {"1", 1}, {"9", 1}, {"10", 2}, {"99", 2}, {"100", 3}, {"9999", 4}, {"10000", 5}} {
		var value expDecimal
		value.coefficient.SetString(tc.input, 10)
		if got := w.digits(&value.coefficient); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
