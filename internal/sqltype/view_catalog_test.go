package sqltype

import (
	"context"
	"testing"
	"unicode/utf8"
)

func TestViewCatalogTypes(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id              ID
		database, input string
	}{{ID(26), "OID", "4294967295"}, {ID(18), "CHAR", "v"}} {
		d := types[tc.id]
		if d == nil {
			t.Fatalf("missing catalog type %d", tc.id)
		}
		if d.DatabaseName() != tc.database {
			t.Fatal(d.DatabaseName())
		}
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Output(ctx, value)
		if err != nil || out != tc.input {
			t.Fatal(out, err)
		}
		if !Null(d).IsNull() {
			t.Fatal("lost NULL")
		}
		comparison, err := BindComparison(d, d)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := comparison.Compare(ctx, value, value); err != nil || got != 0 {
			t.Fatal(got, err)
		}
	}
	for _, input := range []string{"4294967296", "-2147483649"} {
		d := types[ID(26)]
		if d == nil {
			t.Fatal("missing OID")
		}
		if _, err := d.Input(ctx, input, -1); err == nil {
			t.Fatal("out-of-domain OID accepted", input)
		}
	}
}

func TestViewCatalogOIDReleaseInputs(t *testing.T) {
	d := testTypes()[ID(26)]
	if d == nil {
		t.Fatal("missing OID")
	}
	for _, tc := range []struct{ input, output string }{{"-1", "4294967295"}, {"-2147483648", "2147483648"}, {"4294967295", "4294967295"}} {
		value, err := d.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		got, err := d.Output(context.Background(), value)
		if err != nil || got != tc.output {
			t.Fatal(tc, got, err)
		}
	}
	numericProviderError(t, func() error { _, err := d.Input(context.Background(), "bad", -1); return err }(), "22P02", `invalid input syntax for type oid: "bad"`)
}

func TestViewCatalogCHARReleaseInputs(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[ID(18)]
	for _, tc := range []struct{ input, output string }{{"", ""}, {"ab", "a"}, {"é", `\303`}, {`\303`, `\303`}, {`\166`, "v"}, {`\000`, ""}} {
		value, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		output, err := d.Output(ctx, value)
		if err != nil || output != tc.output {
			t.Fatal(tc, output, err)
		}
	}
	a, err := d.Input(ctx, "é", -1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Input(ctx, "v", -1)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := comparison.Compare(ctx, a, b); err != nil || result <= 0 {
		t.Fatal(result, err)
	}
}

func TestViewCatalogCHARSQLLiteralHighByte(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Char]
	value, err := d.Input(ctx, "é", -1)
	if err != nil {
		t.Fatal(err)
	}
	literal, err := d.SQLLiteral(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(literal) {
		t.Fatalf("SQLLiteral is invalid UTF-8: %x", literal)
	}
	if literal != `'\303'` {
		t.Fatalf("SQLLiteral = %q, want printable octal literal", literal)
	}
	replayed, err := d.Input(ctx, literal[1:len(literal)-1], -1)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.text != value.text || replayed.text != "\xc3" {
		t.Fatalf("replayed CHAR payload = %x, original = %x", replayed.text, value.text)
	}
}

func TestViewLiteralRetagPreservesScalar(t *testing.T) {
	ctx := context.Background()
	oldTypes, nextTypes := testTypes(), testTypes()
	for _, tc := range []struct {
		id     ID
		input  string
		typmod int32
	}{
		{Bool, "true", -1}, {Bytea, `\x00ff`, -1}, {Char, "é", -1},
		{Name, "MiXeDé", -1}, {Int8, "-9223372036854775808", -1},
		{Int2, "-32768", -1}, {Int4, "2147483647", -1}, {Text, "x'y", -1},
		{Oid, "4294967295", -1}, {Float4, "-0.0", -1}, {Float4, "NaN", -1},
		{Float8, "Infinity", -1}, {BPChar, "xy", 8}, {VarChar, "xy", 8},
		{Date, "2000-02-29", -1}, {Date, "-infinity", -1},
		{Time, "24:00", -1}, {Time, "04:05:06.1235", 3},
		{Timestamp, "2000-01-02 04:05:06.1235", 3},
		{Timestamp, "294276-12-31 23:59:59.999999", 0},
		{Numeric, "001.2300", -1}, {Numeric, "NaN", -1}, {Regclass, "42", -1},
		{UUID, "550e8400-e29b-41d4-a716-446655440000", -1},
	} {
		old, next := oldTypes[tc.id], nextTypes[tc.id]
		value, err := old.Input(ctx, tc.input, tc.typmod)
		if err != nil {
			t.Fatal(tc, err)
		}
		for _, original := range []Value{value, Null(old)} {
			retagged, err := original.Retag(next)
			if err != nil || retagged.Type() != next || original.Type() != old {
				t.Fatal(tc, retagged, original, err)
			}
			roundtrip, err := retagged.Retag(old)
			if err != nil || !SameDatum(original, roundtrip) {
				t.Fatal("scalar payload changed", tc, err)
			}
			if original.IsNull() {
				if !retagged.IsNull() {
					t.Fatal("lost typed NULL", tc)
				}
				continue
			}
			before, err := old.Output(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			after, err := next.Output(ctx, retagged)
			if err != nil || before != after {
				t.Fatal(tc, before, after, err)
			}
		}
	}
	value := Boolean(oldTypes[Bool], true)
	for _, target := range []*Descriptor{nil, nextTypes[Unknown], nextTypes[Int4]} {
		if _, err := value.Retag(target); err == nil {
			t.Fatal("incompatible identity accepted", target)
		}
	}
	if _, err := (Value{}).Retag(nextTypes[Bool]); err == nil {
		t.Fatal("invalid scalar accepted")
	}
}
