package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntegerRangeColumns(t *testing.T) {
	cases := []struct{ input, output string }{
		{"[1,4]", "[1,5)"}, {"(1,4)", "[2,4)"}, {"(1,4]", "[2,5)"},
		{"[4,4]", "[4,5)"}, {"[4,4)", "empty"}, {"(4,4]", "empty"},
		{"(4,4)", "empty"}, {"(4,5)", "empty"}, {"empty", "empty"},
		{" \tEMPTY\r\n", "empty"}, {"[,]", "(,)"}, {"(,)", "(,)"},
		{"[,)", "(,)"}, {"(,]", "(,)"}, {"[,4]", "(,5)"}, {"[1,]", "[1,)"},
		{" [ +0001 , 0x_F ] ", "[1,16)"}, {`["1","4"]`, "[1,5)"},
		{`["1"2,14)`, "[12,14)"}, {`[\1,4]`, "[1,5)"},
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				create := fixture.Exec("CREATE TABLE r(k integer,a int4range,b pg_catalog.int8range)", 0)
				create.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, create)
				want := make([][]any, 0, len(cases)+10)
				for i, tc := range cases {
					literal := rangeLiteral(tc.input)
					s := fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES (%d,%s,%s)", i, literal, literal), 1)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					want = append(want, []any{int64(i), tc.output, tc.output})
				}
				extrema := []struct{ a, b, outA, outB string }{
					{"[-2147483648,2147483647)", "[-9223372036854775808,9223372036854775807)", "[-2147483648,2147483647)", "[-9223372036854775808,9223372036854775807)"},
					{"(-2147483648,2147483647)", "(-9223372036854775808,9223372036854775807)", "[-2147483647,2147483647)", "[-9223372036854775807,9223372036854775807)"},
					{"[2147483647,)", "[9223372036854775807,)", "[2147483647,)", "[9223372036854775807,)"},
					{"(,-2147483648)", "(,-9223372036854775808)", "(,-2147483648)", "(,-9223372036854775808)"},
					{"[2147483647,2147483647)", "[9223372036854775807,9223372036854775807)", "empty", "empty"},
					{"(2147483647,2147483647]", "(9223372036854775807,9223372036854775807]", "empty", "empty"},
				}
				for i, tc := range extrema {
					k := len(cases) + i
					s := fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES (%d,%s,%s)", k, rangeLiteral(tc.a), rangeLiteral(tc.b)), 1)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					want = append(want, []any{int64(k), tc.outA, tc.outB})
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (100,NULL,NULL)", 1))
				want = append(want, []any{int64(100), nil, nil})
				s := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "INT4RANGE", "INT8RANGE"}, want, []bool{true, true, true})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		})
	}
}
