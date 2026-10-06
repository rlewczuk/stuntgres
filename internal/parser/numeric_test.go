package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestNumericLiteralSyntaxAndModifierList(t *testing.T) {
	for _, tc := range []struct {
		token   string
		kind    ValueKind
		integer int64
	}{{"-2147483648", IntegerValue, -2147483648}, {"-9223372036854775808", IntegerValue, -9223372036854775808}, {"9223372036854775808", NumericValue, 0}, {"-9223372036854775809", NumericValue, 0}, {"0x8000000000000000", NumericValue, 0}, {"-0x8000000000000000", IntegerValue, -9223372036854775808}, {"0x_FF", IntegerValue, 255}, {"1_000", IntegerValue, 1000}, {"010", IntegerValue, 10}, {"1.", NumericValue, 0}, {".001", NumericValue, 0}, {"1e0", NumericValue, 0}, {"1.618_034", NumericValue, 0}, {"1e1_0", NumericValue, 0}} {
		stmts, err := Parse("SELECT " + tc.token + " FROM t")
		if err != nil {
			t.Fatal(tc, err)
		}
		value := stmts[0].(*Select).Targets[0].(LiteralExpr).Value
		if value.Kind != tc.kind || value.Integer != tc.integer {
			t.Fatal(tc, value)
		}
		if tc.kind == NumericValue && value.Text != tc.token {
			t.Fatal(value)
		}
	}
	stmts, err := Parse("CREATE TABLE n(a NUMERIC(2,-3),b DECIMAL(3,5),c DEC(1000,1000)); SELECT .5 FROM n")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]int32{{2, -3}, {3, 5}, {1000, 1000}} {
		raw := stmts[0].(*CreateTable).Elements[i].Column.TypeName
		if len(raw.Modifiers) != 2 {
			t.Fatal(raw)
		}
		for j, p := range want {
			if raw.Modifiers[j].Integer == nil || *raw.Modifiers[j].Integer != p {
				t.Fatal(raw)
			}
		}
	}
	for _, token := range []string{"1.2__3", "1._2", "1_.2", "1e_2", "1e+", "1e+_2", "0x1g", "1.2z"} {
		_, err := Parse("SELECT " + token + " FROM t")
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42601" || !strings.Contains(got.Message, "numeric literal") || got.Position != "8" {
			t.Fatal(token, got, err)
		}
	}
	// numericfail returns the integer before a distinct DOT_DOT token, not a
	// qualified-column dot. Whole-input errors retain its exact spelling.
	for _, q := range []string{"SELECT 1..2 FROM t", "SELECT .1..2 FROM t"} {
		_, err := Parse(q)
		var got *sqlstate.Error
		position := "9"
		if strings.HasPrefix(q, "SELECT .1") {
			position = "10"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ".."`, Position: position}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(q, got, err, want)
		}
	}
	for _, literal := range []string{"1.5", ".5", "1e2"} {
		_, err := Parse("SET application_name TO " + literal)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "0A000" || got.Message != "non-decimal-integer literal is unsupported" {
			t.Fatal(got, err)
		}
	}
}
