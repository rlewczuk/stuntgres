package sqltype

import (
	"context"
	"strings"
	"testing"
)

func TestIssnCodec(t *testing.T) {
	d, err := Issn(60000, "issn", "public.issn")
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output, literal string }{
		{"12345679", 19542469134006, "1234-5679", "'9771234567003'"},
		{"12345670!", 19542469134007, "1234-5679!", "'9771234567003!'"},
		{"9771234567898", 19542469135796, "977-1234-567-89-8", "'9771234567898'"},
		{"9771234567898!", 19542469135797, "977-1234-567-89-8!", "'9771234567898!'"},
		{"1234567?!", 19542469134006, "1234-5679", "'9771234567003'"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil || v.integer != tc.packed || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v, err) }
		if output, err := d.Output(ctx, v); err != nil || output != tc.output { t.Fatal(output, err) }
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != tc.literal { t.Fatal(literal, err) }
		for _, text := range []string{tc.output, strings.Trim(literal, "'")} {
			again, err := d.Input(ctx, text, -1)
			if err != nil || !SameDatum(v, again) { t.Fatal("lossy roundtrip", tc.input, text, err) }
		}
	}
	for _, input := range []string{"", "1234567", "123456789", "12345670", "11448750", "9771234567000", "9780123456786", "9790123456785", "0123456789012", "1234567890128", "12345678901234", "123\t45679", "123\n45679", "12345679!!", "123456?9", "1144875X ", "1144875? "} {
		if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid input admitted", input) }
	}
	if !Null(d).ValidResult(d.Usage()) { t.Fatal("typed NULL rejected") }
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(cancelled, "12345679", -1); err != context.Canceled { t.Fatal(err) }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Kind != "b" || facts.Category != "U" || facts.Length != 8 || !facts.ByValue || facts.Alignment != "d" || facts.Storage != "p" || facts.Modifier != -1 || facts.Collation != 0 { t.Fatal("scalar metadata", facts, ok) }
}

func TestIssnFormatting(t *testing.T) {
	d, err := Issn(60000, "issn", "public.issn")
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ input, output string }{
		{"12345679", "1234-5679"}, {"1234-5679", "1234-5679"}, {"9771234567003", "1234-5679"},
		{"1436-4522", "1436-4522"}, {"1144875x", "1144-875X"}, {"1144875X", "1144-875X"},
		{"1144875?", "1144-875X"}, {"3251231?", "3251-2317"}, {"00000000", "0000-0000"}, {"99999994", "9999-9994"},
		{"12345679 ", "1234-5679"}, {"12345679-", "1234-5679"}, {"12345670!", "1234-5679!"},
		{"12345679!", "1234-5679!"}, {"9771234567000!", "1234-5679!"}, {"1234567?!", "1234-5679"},
		{"9771234567898", "977-1234-567-89-8"}, {"9771234567898!", "977-1234-567-89-8!"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1)
		if err != nil { t.Fatal(tc.input, err) }
		if output, err := d.Output(context.Background(), v); err != nil || output != tc.output { t.Fatal(tc.input, output, err) }
	}
}

func TestIssnIdentity(t *testing.T) {
	d, err := Issn(60000, "issn", "public.issn")
	if err != nil { t.Fatal(err) }
	foreign, err := Issn(d.ID(), "renamed", "other.renamed")
	if err != nil { t.Fatal(err) }
	if !d.IsIssn() || d.IsEan13() || d.IsIsbn() || d.IsIsbn13() || d.IsIsmn() || d.IsIsmn13() || !d.IsUserDefined() || d.DatabaseName() != "ISSN" { t.Fatal("distinct scalar role") }
	v, err := d.Input(context.Background(), "9771234567898", -1)
	if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "9771234567898!", -1)
	if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		for _, construct := range []func(ID, string, string) (*Descriptor, error){Ean13, Isbn, Isbn13, Ismn, Ismn13} {
			sibling, err := construct(d.ID(), "issn", "public.issn")
			if err != nil { t.Fatal(err) }
			if _, err := source.Retag(sibling); err == nil { t.Fatal("sibling retagged") }
			if _, err := source.RemapIssn(sibling); err == nil { t.Fatal("sibling imported") }
			if source.ValidResult(sibling.Usage()) { t.Fatal("sibling result admitted") }
		}
		mapped, err := source.RemapIssn(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() || mapped.integer != source.integer || SameDatum(source, mapped) { t.Fatal("import changed payload or granted identity", mapped, err) }
	}
	for _, packed := range []int64{-1, 0, 19999999999999, 19560246913572, 19542469134000, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid payload admitted", packed) }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid output admitted") }
		if _, err := d.SQLLiteral(context.Background(), bad); err == nil { t.Fatal("invalid serialization admitted") }
		if _, err := bad.RemapIssn(foreign); err == nil { t.Fatal("invalid import admitted") }
	}
}

func TestIssnCapabilities(t *testing.T) {
	for _, tc := range []struct{ id ID; name, declaration string }{{0,"issn","issn"}, {Unknown,"issn","issn"}, {60000,"","issn"}, {60000,"issn",""}} {
		if _, err := Issn(tc.id, tc.name, tc.declaration); err == nil { t.Fatal("invalid descriptor admitted", tc) }
	}
	d, err := Issn(60000, "issn", "public.issn")
	if err != nil { t.Fatal(err) }
	array := LookupArray(60001, "_issn", d)
	if array.Concrete() || array.Element() != d { t.Fatal("companion gained execution") }
	if _, err := array.Input(context.Background(), "{12345679}", -1); err == nil { t.Fatal("lookup-only array input admitted") }
	if d.CheckBinaryInput() == nil || d.CheckBinaryOutput() == nil { t.Fatal("ISSN gained binary codecs") }
	if equality, ordering := ComparisonCapabilities(d); equality || ordering { t.Fatal("ISSN gained comparison capability") }
	for _, construct := range []func(ID, string, string) (*Descriptor, error){Ean13, Isbn, Isbn13, Ismn, Ismn13} {
		sibling, err := construct(60002, "sibling", "public.sibling")
		if err != nil { t.Fatal(err) }
		if _, err := PlanCoercion(d.Usage(), sibling.Usage(), Explicit); err == nil { t.Fatal("sibling cast admitted") }
	}
}
