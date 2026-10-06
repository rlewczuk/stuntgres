package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTimestampGenericNumericPrecisionInputAndSkip(t *testing.T) {
	for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
		for _, tc := range []struct{ number, raw, code string }{
			{".5", ".5", "22P02"}, {"1.5e+3", "1.5e+3", "22P02"}, {"1e+2", "1e+2", "22P02"}, {"1.", "1.", "22P02"},
			{"1_0.5", "1_0.5", "22P02"}, {"1.5e+3_0", "1.5e+3_0", "22P02"},
			{"1.5", "1.5", "22P02"}, {"1e2", "1e2", "22P02"},
			{"+.5", ".5", "42601"}, {"-1.5e+3", "-1.5e+3", "22P02"}, {"+1e+2", "1e+2", "42601"}, {"-1.", "-1.", "22P02"},
			{"+3", "3", "42601"}, {"+0", "0", "42601"}, {"+2147483648", "2147483648", "42601"},
			{"+7", "7", "42601"},
			{"2147483648", "2147483648", "22003"}, {"2147483649", "2147483649", "22003"},
			{"-2147483649", "-2147483649", "22003"}, {"999999999999999999999999999999", "999999999999999999999999999999", "22003"},
			{"2_147_483_648", "2_147_483_648", "22003"}, {"0x80000000", "0x80000000", "22003"},
			{"0o20000000000", "0o20000000000", "22003"}, {"0b10000000000000000000000000000000", "0b10000000000000000000000000000000", "22003"},
			{"-2147483648", "-2147483648", "22023"},
		} {
			number := tc.number
			for _, table := range []string{"bad", `"é"`} {
				e := New()
				q := "CREATE TABLE " + table + "(v " + head + "(" + number + "))"
				ast := statement(t, q)
				if _, err := testDescribe(e, ast); err != nil {
					t.Fatal("utility Prepare must defer modifier input", q, err)
				}
				_, err := testExecute(e, context.Background(), ast, nil)
				var got *sqlstate.Error
				message := `invalid input syntax for type integer: "` + tc.raw + `"`
				if tc.code == "22003" {
					message = `value "` + tc.raw + `" is out of range for type integer`
				}
				if tc.code == "22023" {
					message = "TIMESTAMP(-2147483648) precision must not be negative"
				}
				if tc.code == "42601" {
					message = "type modifiers must be simple constants or identifiers"
				}
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: message, Position: "20"}
				if !errors.As(err, &got) || *got != want {
					t.Fatal(q, got, err, want)
				}
				run(t, e, "CREATE TABLE "+table+"(v TIMESTAMP)")
				// Destination skip precedes semantic int4 input, but not scanner junk.
				run(t, e, "CREATE TABLE IF NOT EXISTS "+table+"(v "+head+"("+number+"))")
				if tc.number == ".5" {
					bad, err := parser.Parse("CREATE TABLE IF NOT EXISTS " + table + "(v " + head + "(1.5foo))")
					position := "46"
					if head != `"timestamp"` {
						position = "52"
					}
					wantJunk := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: position}
					if bad != nil || !errors.As(err, &got) || *got != wantJunk {
						t.Fatal(bad, got, err, wantJunk)
					}
				}
			}
		}
	}
}
