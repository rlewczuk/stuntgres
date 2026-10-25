package sqltype

import (
	"context"
	"testing"
)

func TestIsmn13Codec(t *testing.T) {
	d, err := Ismn13(60000, "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output string }{
		{"M123456785", 19580246913570, "979-0-1234-5678-5"},
		{"m-1234-5678-5", 19580246913570, "979-0-1234-5678-5"},
		{"9790123456785", 19580246913570, "979-0-1234-5678-5"},
		{"M123456780!", 19580246913571, "979-0-1234-5678-5!"},
		{"M123456785!", 19580246913571, "979-0-1234-5678-5!"},
		{"M12345678X!", 19580246913571, "979-0-1234-5678-5!"},
		{"M12345678?", 19580246913570, "979-0-1234-5678-5"},
		{"M12345678?!", 19580246913570, "979-0-1234-5678-5"},
		{"979012345678?!", 19580246913570, "979-0-1234-5678-5"},
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

func TestIsmn13Formatting(t *testing.T) {
	d, err := Ismn13(60000, "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ input, output string }{
		{"9790123456785", "979-0-1234-5678-5"},
		{"979-0-1234-5678-5", "979-0-1234-5678-5"},
		{"M123456785", "979-0-1234-5678-5"},
		{"m-1234-5678-5", "979-0-1234-5678-5"},
		{"M-3452-4680-5", "979-0-3452-4680-5"},
		{"9790345246805", "979-0-3452-4680-5"},
		{"M12345678?", "979-0-1234-5678-5"},
		{"979012345678?", "979-0-1234-5678-5"},
		{"M123456780!", "979-0-1234-5678-5!"},
		{"9790123456780!", "979-0-1234-5678-5!"},
		{"M123456785!", "979-0-1234-5678-5!"},
		{"M12345678X!", "979-0-1234-5678-5!"},
		{"M12345678?!", "979-0-1234-5678-5"},
		{"979012345678?!", "979-0-1234-5678-5"},
		{" M 1234-5678-5 -", "979-0-1234-5678-5"},
		{"9790123456785 -", "979-0-1234-5678-5"},
		{"M000000001", "979-0-000-00000-1"},
		{"9790000000001", "979-0-000-00000-1"},
		{"M099999996", "979-0-099-99999-6"},
		{"M100000000", "979-0-1000-0000-0"},
		{"M399999993", "979-0-3999-9999-3"},
		{"M400000007", "979-0-40000-000-7"},
		{"M699999990", "979-0-69999-999-0"},
		{"M700000004", "979-0-700000-00-4"},
		{"M899999998", "979-0-899999-99-8"},
		{"M900000002", "979-0-9000000-0-2"},
		{"M999999997", "979-0-9999999-9-7"},
		{"9790999999997", "979-0-9999999-9-7"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if out, err := d.Output(context.Background(), v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
	}
}

func TestIsmn13Identity(t *testing.T) {
	d, err := Ismn13(60000, "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	foreign, err := Ismn13(d.ID(), "renamed", "other.renamed"); if err != nil { t.Fatal(err) }
	ean, err := Ean13(d.ID(), "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	isbn, err := Isbn(d.ID(), "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	short, err := Ismn(d.ID(), "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	long, err := Isbn13(d.ID(), "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	if !d.IsIsmn13() || d.IsIsmn() || d.IsIsbn13() || d.IsIsbn() || d.IsEan13() || !d.IsUserDefined() || d.DatabaseName() != "ISMN13" { t.Fatal("distinct scalar role") }
	v, err := d.Input(context.Background(), "M123456785", -1); if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "M123456785!", -1); if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		for _, sibling := range []*Descriptor{ean, isbn, long, short} {
			if _, err := source.Retag(sibling); err == nil { t.Fatal("sibling family retagged") }
			if _, err := source.RemapIsmn13(sibling); err == nil { t.Fatal("sibling family imported") }
			if source.ValidResult(sibling.Usage()) { t.Fatal("sibling result accepted") }
		}
		mapped, err := source.RemapIsmn13(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() || mapped.integer != source.integer { t.Fatal(mapped, err) }
		if SameDatum(source, mapped) { t.Fatal("foreign pointer granted datum identity") }
	}
	for _, packed := range []int64{-1, 0, 19999999999999, 19560246913572, 19580246913560, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid packed family admitted", packed) }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid packed output admitted") }
		if _, err := bad.RemapIsmn13(foreign); err == nil { t.Fatal("invalid packed import admitted") }
	}
}

func TestIsmn13Capabilities(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ id ID; name, declaration string }{
		{0, "ismn13", "ismn13"}, {Unknown, "ismn13", "ismn13"}, {60000, "", "ismn13"}, {60000, "ismn13", ""},
	} {
		if _, err := Ismn13(tc.id, tc.name, tc.declaration); err == nil { t.Fatal("invalid descriptor admitted", tc) }
	}
	d, err := Ismn13(60000, "ismn13", "public.ismn13"); if err != nil { t.Fatal(err) }
	array := LookupArray(60001, "_ismn13", d)
	if array.Concrete() || array.Element() != d { t.Fatal("companion gained execution") }
	if _, err := array.Input(ctx, "{M123456785}", -1); err == nil { t.Fatal("lookup-only array input admitted") }
	if d.CheckBinaryInput() == nil || d.CheckBinaryOutput() == nil { t.Fatal("ISMN gained binary codecs") }
	if equality, ordering := ComparisonCapabilities(d); equality || ordering { t.Fatal("ISMN gained comparison capability") }
	for _, constructor := range []func(ID, string, string) (*Descriptor, error){Ean13, Isbn, Isbn13, Ismn} {
		sibling, err := constructor(60002, "sibling", "public.sibling"); if err != nil { t.Fatal(err) }
		if _, err := PlanCoercion(d.Usage(), sibling.Usage(), Explicit); err == nil { t.Fatal("sibling cast admitted") }
	}
}
