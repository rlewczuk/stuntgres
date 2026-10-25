package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func numericRangeValues() []struct{ input, output string } {
	return []struct{ input, output string }{
		{"[1.25,4.7500]", "[1.25,4.7500]"}, {"(1.25,4.7500)", "(1.25,4.7500)"},
		{"[1.25,4.7500)", "[1.25,4.7500)"}, {"(1.25,4.7500]", "(1.25,4.7500]"},
		{"(4,5)", "(4,5)"}, {"[4,4]", "[4,4]"}, {"[4,4)", "empty"}, {"(4,4]", "empty"}, {"(4,4)", "empty"},
		{"[1.0,1.00]", "[1.0,1.00]"}, {"[1.0,1.00)", "empty"},
		{"[001.2300,2e1]", "[1.2300,20]"}, {"[-0.000,1.230e2)", "[0.000,123.0)"}, {"[0x_F,16.00]", "[15,16.00]"},
		{" \tEMPTY\r\n", "empty"}, {"[,]", "(,)"}, {"(,]", "(,)"}, {"[,)", "(,)"}, {"(,)", "(,)"},
		{"[,4.00]", "(,4.00]"}, {"[1.00,]", "[1.00,)"},
		{"[-inf,+INF]", "[-Infinity,Infinity]"}, {"(Infinity,)", "(Infinity,)"}, {"[Infinity,Infinity]", "[Infinity,Infinity]"},
		{"[Infinity,Infinity)", "empty"}, {"[-Infinity,-Infinity]", "[-Infinity,-Infinity]"},
		{"[Infinity,NaN]", "[Infinity,NaN]"}, {"[NaN,NaN]", "[NaN,NaN]"}, {"[NaN,NaN)", "empty"},
		{"[,-Infinity]", "(,-Infinity]"}, {"[-Infinity,)", "[-Infinity,)"}, {"[,Infinity]", "(,Infinity]"}, {"[NaN,)", "[NaN,)"},
		{"[9007199254740992.0000000000000001,9007199254740992.0000000000000002]", "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]"},
		{"[9223372036854775808.0001,9223372036854775808.0002)", "[9223372036854775808.0001,9223372036854775808.0002)"},
		{`["1.25","4.7500"]`, "[1.25,4.7500]"}, {`["1"2,14)`, "[12,14)"}, {`[\1,4]`, "[1,4]"},
	}
}

func TestReferenceNumericRangeColumns(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				s := fixture.Exec("CREATE TABLE r(k integer NOT NULL,v pg_catalog.numrange)", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				want := [][]any{}
				for i, tc := range numericRangeValues() {
					s = fixture.Exec(fmt.Sprintf("INSERT INTO r VALUES (%d,%s)", i+1, rangeLiteral(tc.input)), 1)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					want = append(want, []any{int64(i+1), tc.output})
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (100,NULL)", 1))
				want = append(want, []any{int64(100), nil})
				s = rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "NUMRANGE"}, want, []bool{false, true})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		})
	}
}
