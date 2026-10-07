package stuntgres_test

import (
	"reflect"
	"testing"

	"stuntgres"
)

func TestMathNumericRoundScale(t *testing.T) {
	mathPublicValues(t, []struct {
		q    string
		want any
	}{
		{"SELECT round(2.5)", "3"}, {"SELECT round(-2.5)", "-3"},
		{"SELECT round(1.25,1)", "1.3"}, {"SELECT round(-1.25,1)", "-1.3"},
		{"SELECT trunc(1.25,1)", "1.2"}, {"SELECT trunc(-1.25,1)", "-1.2"},
		{"SELECT round(9.995,2)", "10.00"}, {"SELECT round(-9.995,2)", "-10.00"},
		{"SELECT round(1500,-3)", "2000"}, {"SELECT round(-1500,-3)", "-2000"},
		{"SELECT trunc(-1500,-3)", "-1000"}, {"SELECT trunc(1.23,4)", "1.2300"},
		{"SELECT scale(8.4100)", int64(4)}, {"SELECT min_scale(8.4100)", int64(2)}, {"SELECT trim_scale(8.4100)", "8.41"},
		{"SELECT scale(0.0000)", int64(4)}, {"SELECT min_scale(0.0000)", int64(0)}, {"SELECT trim_scale(0.0000)", "0"},
		{"SELECT scale('NaN')", nil}, {"SELECT min_scale('-Infinity')", nil}, {"SELECT trim_scale('NaN')", "NaN"},
		{"SELECT round('Infinity',2)", "Infinity"}, {"SELECT trunc('-Infinity',2)", "-Infinity"},
		{"SELECT round(NULL)", nil}, {"SELECT trunc(NULL,2)", nil}, {"SELECT trim_scale(NULL)", nil},
	})
}

func TestMathSignModulo(t *testing.T) {
	mathPublicValues(t, []struct {
		q    string
		want any
	}{
		{"SELECT sign(-13.000)", "-1"}, {"SELECT sign(0.0000)", "0"}, {"SELECT sign(13.000)", "1"},
		{"SELECT sign(COALESCE('NaN',1.0))", "NaN"}, {"SELECT sign(COALESCE('-Infinity',1.0))", "-1"},
		{"SELECT sign('NaN')", float64(0)}, {"SELECT sign('-0')", float64(0)},
		{"SELECT mod(-9,4)", int64(-1)}, {"SELECT mod(9,-4)", int64(1)}, {"SELECT mod(-9,-4)", int64(-1)},
		{"SELECT mod(-2147483648,-1)", int64(0)}, {"SELECT mod(-9223372036854775808,-1)", int64(0)},
		{"SELECT mod(-9.00,4.0)", "-1.00"}, {"SELECT mod(9.00,-4.000)", "1.000"},
		{"SELECT mod(8.00,4.000)", "0.000"}, {"SELECT mod(0.0000,4.0)", "0.0000"},
		{"SELECT mod(9007199254740993.25,2.0)", "1.25"}, {"SELECT mod(-9007199254740993.25,2.0)", "-1.25"},
		{"SELECT mod(9223372036854775808,3)", "2"},
		{"SELECT mod('NaN',0.0)", "NaN"}, {"SELECT mod(0.0,'NaN')", "NaN"},
		{"SELECT mod('Infinity',1.0)", "NaN"}, {"SELECT mod(1.2300,'Infinity')", "1.2300"},
		{"SELECT sign(NULL)", nil}, {"SELECT mod(1,NULL)", nil}, {"SELECT mod(NULL,1)", nil},
	})
}

func mathPublicValues(t *testing.T, cases []struct {
	q    string
	want any
}) {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range cases {
		var got any
		if err := db.QueryRow(tc.q).Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s direct: %#v %v", tc.q, got, err)
		}
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		err = s.QueryRow().Scan(&got)
		s.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s prepared: %#v %v", tc.q, got, err)
		}
	}
}
