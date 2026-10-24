package sqltype

import (
	"context"
	"testing"
)

func TestIsbn13Codec(t *testing.T) {
	d, err := Isbn13(60000, "isbn13", "public.isbn13"); if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output string }{
		{"9780123456786", 19560246913572, "978-0-12-345678-6"},
		{"9780123456780!", 19560246913573, "978-0-12-345678-6!"},
		{"123456789X", 19562469135794, "978-1-234-56789-7"},
		{"123456789x", 19562469135794, "978-1-234-56789-7"},
		{"1234567890!", 19562469135795, "978-1-234-56789-7!"},
		{"123456789X!", 19562469135795, "978-1-234-56789-7!"},
		{"123456789?", 19562469135794, "978-1-234-56789-7"},
		{"123456789?!", 19562469135794, "978-1-234-56789-7"},
		{"978012345678?", 19560246913572, "978-0-12-345678-6"},
	} {
		v, err := d.Input(ctx, tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if v.integer != tc.packed || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v) }
		if out, err := d.Output(ctx, v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
		again, err := d.Input(ctx, tc.output, -1); if err != nil || !SameDatum(v, again) { t.Fatal("canonical roundtrip", tc.input, err) }
	}
	for _, input := range []string{"", "1", "12345678901", "123456A89X", "123\t456789X", "123\n456789X", "!123456789X", "123456789X!!", "12345678?X", "123456789Xjunk", "123456789X ", "123456789X-", "1234567890", "9790123456785", "9790123456780!", "9771234567898", "0123456789012", "1234567890128", "12345678901234"} {
		if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid input admitted", input) }
	}
	if !Null(d).ValidResult(d.Usage()) { t.Fatal("typed NULL rejected") }
	cancelled, cancel := context.WithCancel(ctx); cancel()
	if _, err := d.Input(cancelled, "123456789X", -1); err != context.Canceled { t.Fatal(err) }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Kind != "b" || facts.Category != "U" || facts.Length != 8 || !facts.ByValue || facts.Alignment != "d" || facts.Storage != "p" || facts.Modifier != -1 { t.Fatal("scalar metadata", facts, ok) }
}

func TestIsbn13Formatting(t *testing.T) {
	d, err := Isbn13(60000, "isbn13", "public.isbn13"); if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ input, output string }{
		{"0000000000", "978-0-00-000000-2"},
		{"9780200000000", "978-0-200-00000-0"},
		{"9780700000005", "978-0-7000-0000-5"},
		{"9780950000008", "978-0-9500000-0-8"},
		{"9791000000008", "979-10-00-00000-8"},
		{"9791234567896", "979-123456789-6"},
		{"978-0-393-04002-9", "978-0-393-04002-9"},
		{"1 234-56789X", "978-1-234-56789-7"},
		{"9780123456786 -", "978-0-12-345678-6"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if out, err := d.Output(context.Background(), v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
	}
}

func TestIsbn13Identity(t *testing.T) {
	d, err := Isbn13(60000, "isbn13", "public.isbn13"); if err != nil { t.Fatal(err) }
	foreign, err := Isbn13(d.ID(), "renamed", "other.renamed"); if err != nil { t.Fatal(err) }
	ean, err := Ean13(d.ID(), "isbn13", "public.isbn13"); if err != nil { t.Fatal(err) }
	isbn, err := Isbn(d.ID(), "isbn13", "public.isbn13"); if err != nil { t.Fatal(err) }
	if !d.IsIsbn13() || d.IsIsbn() || d.IsEan13() || !d.IsUserDefined() || d.DatabaseName() != "ISBN13" { t.Fatal("distinct scalar role") }
	v, err := d.Input(context.Background(), "123456789X", -1); if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "123456789X!", -1); if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		for _, sibling := range []*Descriptor{ean, isbn} {
			if _, err := source.Retag(sibling); err == nil { t.Fatal("sibling family retagged") }
			if _, err := source.RemapIsbn13(sibling); err == nil { t.Fatal("sibling family imported") }
			if source.ValidResult(sibling.Usage()) { t.Fatal("sibling result accepted") }
		}
		mapped, err := source.RemapIsbn13(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() || mapped.integer != source.integer { t.Fatal(mapped, err) }
		if SameDatum(source, mapped) { t.Fatal("foreign pointer granted datum identity") }
	}
	for _, packed := range []int64{-1, 0, 19999999999999, 19580246913570, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid packed family admitted", packed) }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid packed output admitted") }
		if _, err := bad.RemapIsbn13(foreign); err == nil { t.Fatal("invalid packed import admitted") }
	}
}
