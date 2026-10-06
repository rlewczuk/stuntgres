package parser

import (
	"strings"
	"testing"
)

// Release FCONST spellings are kept verbatim for int4 modifier input. Unary
// minus prefixes the numeric token; plus retains a distinct nonsimple syntax
// kind with its operand spelling rather than becoming a constant.
func TestTimestampGenericNumericPrecisionRawTokens(t *testing.T) {
	for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
		for _, tc := range []struct{ sql, raw string }{
			{".5", ".5"}, {"1.5e+3", "1.5e+3"}, {"1e+2", "1e+2"}, {"1.", "1."},
			{"1_0.5", "1_0.5"}, {"1.5e+3_0", "1.5e+3_0"},
			{"1.5", "1.5"}, {"1e2", "1e2"},
			{"+.5", ".5"}, {"-1.5e+3", "-1.5e+3"}, {"+1e+2", "1e+2"}, {"-1.", "-1."},
			{"2147483648", "2147483648"}, {"2147483649", "2147483649"},
			{"-2147483649", "-2147483649"}, {"999999999999999999999999999999", "999999999999999999999999999999"},
			{"2_147_483_648", "2_147_483_648"}, {"0x80000000", "0x80000000"},
			{"0o20000000000", "0o20000000000"}, {"0b10000000000000000000000000000000", "0b10000000000000000000000000000000"},
		} {
			for _, table := range []string{"bad", `"é"`} {
				q := "CREATE TABLE " + table + "(v " + head + "(" + tc.sql + "))"
				stmts, err := Parse(q)
				if err != nil {
					t.Fatal(q, err)
				}
				raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
				if raw.Modifiers[0].Raw != tc.raw || raw.Modifiers[0].Integer != nil || raw.Pos.Character != 20 || raw.Modifiers[0].UnaryPlus != strings.HasPrefix(tc.sql, "+") {
					t.Fatal(q, raw, tc.raw)
				}
			}
		}
	}
}

func TestTimestampGenericUnaryPlusPrecisionAST(t *testing.T) {
	for _, head := range []string{`"timestamp"`, `U&"t\0069mestamp"`} {
		for _, table := range []string{"bad", `"é"`} {
			for _, number := range []string{"+3", "+0", "+7", "+2147483648"} {
				q := "CREATE TABLE " + table + "(v " + head + "(" + number + "))"
				stmts, err := Parse(q)
				if err != nil {
					t.Fatal(q, err)
				}
				raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
				if !raw.Modifiers[0].UnaryPlus || raw.Pos.Character != 20 {
					t.Fatal(q, raw)
				}
				if number == "+2147483648" {
					if raw.Modifiers[0].Raw != "2147483648" || raw.Modifiers[0].Integer != nil {
						t.Fatal(raw)
					}
				} else {
					want := int32(3)
					if number == "+0" {
						want = 0
					}
					if number == "+7" {
						want = 7
					}
					if raw.Modifiers[0].Integer == nil || *raw.Modifiers[0].Integer != want || raw.Modifiers[0].Raw != "" {
						t.Fatal(raw, want)
					}
				}
			}
		}
	}
}
