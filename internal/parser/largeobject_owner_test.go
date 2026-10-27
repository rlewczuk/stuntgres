package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLargeObjectOwnerNumeric(t *testing.T) {
	const prefix = "/* λ雪 */ ALTER LARGE OBJECT "
	var retained []*AlterOwner
	var numbers []Value
	for _, tc := range []struct { text string; kind ValueKind; integer int64; raw string }{
		{"16", IntegerValue, 16, "16"}, {"+16", IntegerValue, 16, "+16"}, {"-16", IntegerValue, -16, "-16"},
		{"016", IntegerValue, 16, "016"}, {"0x10", IntegerValue, 16, "0x10"}, {"0o20", IntegerValue, 16, "0o20"}, {"0b10000", IntegerValue, 16, "0b10000"}, {"1_6", IntegerValue, 16, "1_6"},
		{"-0", IntegerValue, 0, "-0"}, {"2147483647", IntegerValue, 2147483647, "2147483647"},
		{"2147483648", NumericValue, 0, "2147483648"}, {"-2147483648", NumericValue, 0, "-2147483648"},
		{"4294967295", NumericValue, 0, "4294967295"}, {"+4294967295", NumericValue, 0, "4294967295"},
		{"0x80000000", NumericValue, 0, "0x80000000"}, {"-0x80000000", NumericValue, 0, "-0x80000000"},
		{"4_294_967_295", NumericValue, 0, "4_294_967_295"}, {"040000000000", NumericValue, 0, "040000000000"},
		{"1.0", NumericValue, 0, "1.0"}, {"+.5", NumericValue, 0, ".5"}, {"-1e100", NumericValue, 0, "-1e100"},
		{"999999999999999999999999999999", NumericValue, 0, "999999999999999999999999999999"},
	} {
		text := prefix+tc.text+` OWNER TO "Lo Mixed";`
		statements, err := Parse(text)
		if err != nil || len(statements) != 1 { t.Fatal(text, statements, err) }
		owner, ok := statements[0].(*AlterOwner)
		if !ok || owner.Kind != "large object" || owner.Target.Name != "Lo Mixed" || owner.Target.Identity != "" || owner.Number.Kind != tc.kind || owner.Number.Integer != tc.integer || owner.Number.Text != tc.raw || owner.Number.Pos.Offset != len(prefix) || owner.Number.Pos.Character != utf8.RuneCountInString(prefix)+1 { t.Fatal("NumericOnly AST", text, owner) }
		if owner.Pos.Offset != strings.Index(text, "ALTER") || owner.Target.Pos.Offset != strings.Index(text, `"Lo Mixed"`) { t.Fatal("command/role positions", owner) }
		retained = append(retained, owner)
		numbers = append(numbers, owner.Number)
	}
	// Returned nodes outlive parser scratch reuse and unrelated later calls.
	for range 20 {
		if _, err := Parse("ALTER LARGE OBJECT 999 OWNER TO SESSION_USER"); err != nil { t.Fatal(err) }
	}
	for i, owner := range retained { if !reflect.DeepEqual(owner.Number, numbers[i]) { t.Fatal("parser reuse changed retained operand") } }
	for _, selector := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
		statements, err := Parse("ALTER LARGE OBJECT 16 OWNER TO "+selector)
		if err != nil { t.Fatal(err) }
		want := "current"
		if selector == "SESSION_USER" { want = "session" }
		if statements[0].(*AlterOwner).Target.Identity != want { t.Fatal("role selector resolved at parse time") }
	}
}
