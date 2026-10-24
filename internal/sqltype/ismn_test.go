package sqltype

import (
	"context"
	"testing"
)

func TestIsmnCodec(t *testing.T) {
	d, err := Ismn(60000, "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output string }{
		{"M123456785", 19580246913570, "M-1234-5678-5"},
		{"m-1234-5678-5", 19580246913570, "M-1234-5678-5"},
		{"9790123456785", 19580246913570, "M-1234-5678-5"},
		{"M123456780!", 19580246913571, "M-1234-5678-5!"},
		{"M123456785!", 19580246913571, "M-1234-5678-5!"},
		{"M12345678X!", 19580246913571, "M-1234-5678-5!"},
		{"M12345678?", 19580246913570, "M-1234-5678-5"},
		{"M12345678?!", 19580246913570, "M-1234-5678-5"},
		{"979012345678?!", 19580246913570, "M-1234-5678-5"},
	} {
		v, err := d.Input(ctx, tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if v.integer != tc.packed || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v) }
		if out, err := d.Output(ctx, v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
		again, err := d.Input(ctx, tc.output, -1); if err != nil || !SameDatum(v, again) { t.Fatal("canonical roundtrip", tc.input, err) }
	}
	for _, input := range []string{"", "1", "M12345678", "M12345678901", "M12345678X", "M1234\t56785", "M1234\n56785", "!M123456785", "M123456785!!", "M12345678? ", "M123456780", "9780123456786", "9791234567896", "9771234567898", "0123456789012", "1234567890128", "12345678901234"} {
		if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid input admitted", input) }
	}
	if !Null(d).ValidResult(d.Usage()) { t.Fatal("typed NULL rejected") }
	cancelled, cancel := context.WithCancel(ctx); cancel()
	if _, err := d.Input(cancelled, "M123456785", -1); err != context.Canceled { t.Fatal(err) }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Kind != "b" || facts.Category != "U" || facts.Length != 8 || !facts.ByValue || facts.Alignment != "d" || facts.Storage != "p" || facts.Modifier != -1 { t.Fatal("scalar metadata", facts, ok) }
}

func TestIsmnFormatting(t *testing.T) {
	d, err := Ismn(60000, "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ input, output string }{
		{"9790123456785", "M-1234-5678-5"},
		{"979-0-1234-5678-5", "M-1234-5678-5"},
		{"M123456785", "M-1234-5678-5"},
		{"m-1234-5678-5", "M-1234-5678-5"},
		{"M-3452-4680-5", "M-3452-4680-5"},
		{"9790345246805", "M-3452-4680-5"},
		{"M12345678?", "M-1234-5678-5"},
		{"979012345678?", "M-1234-5678-5"},
		{"M123456780!", "M-1234-5678-5!"},
		{"9790123456780!", "M-1234-5678-5!"},
		{"M123456785!", "M-1234-5678-5!"},
		{"M12345678X!", "M-1234-5678-5!"},
		{"M12345678?!", "M-1234-5678-5"},
		{"979012345678?!", "M-1234-5678-5"},
		{" M 1234-5678-5 -", "M-1234-5678-5"},
		{"9790123456785 -", "M-1234-5678-5"},
		{"M000000001", "M-000-00000-1"},
		{"9790000000001", "M-000-00000-1"},
		{"M099999996", "M-099-99999-6"},
		{"M100000000", "M-1000-0000-0"},
		{"M399999993", "M-3999-9999-3"},
		{"M400000007", "M-40000-000-7"},
		{"M699999990", "M-69999-999-0"},
		{"M700000004", "M-700000-00-4"},
		{"M899999998", "M-899999-99-8"},
		{"M900000002", "M-9000000-0-2"},
		{"M999999997", "M-9999999-9-7"},
		{"9790999999997", "M-9999999-9-7"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if out, err := d.Output(context.Background(), v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
	}
}

func TestIsmnIdentity(t *testing.T) {
	d, err := Ismn(60000, "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	foreign, err := Ismn(d.ID(), "renamed", "other.renamed"); if err != nil { t.Fatal(err) }
	ean, err := Ean13(d.ID(), "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	isbn, err := Isbn(d.ID(), "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	long, err := Isbn13(d.ID(), "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	if !d.IsIsmn() || d.IsIsbn() || d.IsEan13() || !d.IsUserDefined() || d.DatabaseName() != "ISMN" { t.Fatal("distinct scalar role") }
	v, err := d.Input(context.Background(), "M123456785", -1); if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "M123456785!", -1); if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		for _, sibling := range []*Descriptor{ean, isbn, long} {
			if _, err := source.Retag(sibling); err == nil { t.Fatal("sibling family retagged") }
			if _, err := source.RemapIsmn(sibling); err == nil { t.Fatal("sibling family imported") }
			if source.ValidResult(sibling.Usage()) { t.Fatal("sibling result accepted") }
		}
		mapped, err := source.RemapIsmn(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() || mapped.integer != source.integer { t.Fatal(mapped, err) }
		if SameDatum(source, mapped) { t.Fatal("foreign pointer granted datum identity") }
	}
	for _, packed := range []int64{-1, 0, 19999999999999, 19560246913572, 19580246913560, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid packed family admitted", packed) }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid packed output admitted") }
		if _, err := bad.RemapIsmn(foreign); err == nil { t.Fatal("invalid packed import admitted") }
	}
}

func TestIsmnCapabilities(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ id ID; name, declaration string }{
		{0, "ismn", "ismn"}, {Unknown, "ismn", "ismn"}, {60000, "", "ismn"}, {60000, "ismn", ""},
	} {
		if _, err := Ismn(tc.id, tc.name, tc.declaration); err == nil { t.Fatal("invalid descriptor admitted", tc) }
	}
	d, err := Ismn(60000, "ismn", "public.ismn"); if err != nil { t.Fatal(err) }
	array := LookupArray(60001, "_ismn", d)
	if array.Concrete() || array.Element() != d { t.Fatal("companion gained execution") }
	if _, err := array.Input(ctx, "{M123456785}", -1); err == nil { t.Fatal("lookup-only array input admitted") }
	if d.CheckBinaryInput() == nil || d.CheckBinaryOutput() == nil { t.Fatal("ISMN gained binary codecs") }
	if equality, ordering := ComparisonCapabilities(d); equality || ordering { t.Fatal("ISMN gained comparison capability") }
	for _, constructor := range []func(ID, string, string) (*Descriptor, error){Ean13, Isbn, Isbn13} {
		sibling, err := constructor(60002, "sibling", "public.sibling"); if err != nil { t.Fatal(err) }
		if _, err := PlanCoercion(d.Usage(), sibling.Usage(), Explicit); err == nil { t.Fatal("sibling cast admitted") }
	}
}
