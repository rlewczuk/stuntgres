package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitStringFunctionsSlices(t *testing.T) {
	bitStringFunctionsScenario(t, bitStringFunctionsSlices)
}
func TestReferenceBitStringFunctionsBounds(t *testing.T) {
	bitStringFunctionsScenario(t, bitStringFunctionsBounds)
}

func bitStringFunctionsSlices(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE bsf_slice(v VARBIT,r "bit")`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bsf_slice VALUES(B'10110',B'00')", 1))
	for _, tc := range []struct{ expr, want string }{
		{"substring(v FROM 2)", "0110"}, {"substring(v FOR 3)", "101"}, {"substring(v FROM 2 FOR 3)", "011"}, {"substring(v FOR 3 FROM 2)", "011"}, {"substring(v,2,3)", "011"}, {"pg_catalog.substring(v,2)", "0110"}, {`"substring"(v,2,3)`, "011"},
		{"overlay(v PLACING r FROM 3)", "10000"}, {"overlay(v PLACING r FROM 3 FOR 2)", "10000"}, {"pg_catalog.overlay(v,r,3)", "10000"}, {`"overlay"(v,r,3,2)`, "10000"}, {"overlay(v PLACING '00' FROM 3)", "10000"},
		{"overlay(v PLACING B'' FROM 2)", "10110"}, {"overlay(v PLACING B'' FROM 2 FOR 2)", "110"}, {"overlay(v PLACING B'1111' FROM 2 FOR 1)", "11111110"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS result FROM bsf_slice", []string{"result"}, []string{"BIT"}, [][]any{{tc.want}}))
		})
	}
	for _, tc := range []struct {
		source, needle string
		want           int64
	}{
		{"000001101011", "010", 8}, {"111111101", "01", 8}, {"111", "11", 1}, {"0001", "1", 4}, {"101", "000", 0}, {"1", "11", 0}, {"", "", 0}, {"", "1", 0}, {"1", "", 1},
	} {
		for _, expr := range []string{"position(B'" + tc.needle + "' IN B'" + tc.source + "')", "pg_catalog.position(B'" + tc.source + "',B'" + tc.needle + "')", `"position"(B'` + tc.source + `',B'` + tc.needle + `')`} {
			bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS result", []string{"result"}, []string{"INT4"}, [][]any{{tc.want}}))
		}
	}
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT v,r FROM bsf_slice", []string{"v", "r"}, []string{"VARBIT", "BIT"}, [][]any{{"10110", "00"}}))
}

func bitStringFunctionsBounds(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ expr, want string }{
		{"substring(B'10110' FROM 0)", "10110"}, {"substring(B'10110' FROM 0 FOR 3)", "10"}, {"substring(B'10110' FROM -1 FOR 3)", "1"}, {"substring(B'10110' FROM -2 FOR 2)", ""}, {"substring(B'10110' FROM 3 FOR 0)", ""}, {"substring(B'10110' FROM 6)", ""}, {"substring(B'10110' FROM 2 FOR 2147483647)", "0110"}, {"substring(B'10110' FROM 2147483647 FOR 1)", ""}, {"substring(B'10110' FROM -2147483648 FOR 2147483647)", ""},
		{"overlay(B'10110' PLACING B'00' FROM 3 FOR 0)", "1000110"}, {"overlay(B'10110' PLACING B'00' FROM 3 FOR -1)", "10000110"}, {"overlay(B'10110' PLACING B'00' FROM 1 FOR -1)", "0010110"}, {"overlay(B'10110' PLACING B'00' FROM 9 FOR 1)", "1011000"}, {"overlay(B'10110' PLACING B'00' FROM 2147483647 FOR 0)", "1011000"}, {"overlay(B'10110' PLACING B'00' FROM 1 FOR -2147483648)", "0010110"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS result", []string{"result"}, []string{"BIT"}, [][]any{{tc.want}}))
		})
	}
	for _, tc := range []struct{ expr, code, message string }{
		{"substring(B'10110' FROM 99 FOR -1)", "22011", "negative substring length not allowed"},
		{"substring(B'10110' FROM 0 FOR -2147483648)", "22011", "negative substring length not allowed"},
		{"overlay(B'10110' PLACING B'00' FROM 0 FOR -2147483648)", "22011", "negative substring length not allowed"},
		{"overlay(B'10110' PLACING B'00' FROM -1 FOR 2147483647)", "22011", "negative substring length not allowed"},
		{"overlay(B'' PLACING B'' FROM 2147483647 FOR 1)", "22003", "integer out of range"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, bitStringFunctionsError("SELECT "+tc.expr, tc.code, tc.message, 0, prepared, false))
			}
		})
	}
}
