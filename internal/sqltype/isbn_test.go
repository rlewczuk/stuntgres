package sqltype

import (
	"context"
	"testing"
)

func TestIsbnCodec(t *testing.T) {
	d, err := Isbn(60000, "isbn", "public.isbn"); if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct{ input string; packed int64; output string }{
		{"9780123456786", 19560246913572, "0-12-345678-9"},
		{"9780123456780!", 19560246913573, "0-12-345678-9!"},
		{"123456789X", 19562469135794, "1-234-56789-X"},
		{"123456789x", 19562469135794, "1-234-56789-X"},
		{"1234567890!", 19562469135795, "1-234-56789-X!"},
		{"123456789X!", 19562469135795, "1-234-56789-X!"},
		{"123456789?", 19562469135794, "1-234-56789-X"},
		{"123456789?!", 19562469135794, "1-234-56789-X"},
		{"978012345678?", 19560246913572, "0-12-345678-9"},
	} {
		v, err := d.Input(ctx, tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if v.integer != tc.packed || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v) }
		if out, err := d.Output(ctx, v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
	}
	for _, input := range []string{"", "1", "12345678901", "123456A89X", "123\t456789X", "123\n456789X", "!123456789X", "123456789X!!", "12345678?X", "123456789Xjunk", "123456789X ", "123456789X-", "1234567890", "9790123456785", "9771234567898", "0123456789012", "1234567890128", "12345678901234"} {
		if _, err := d.Input(ctx, input, -1); err == nil { t.Fatal("invalid input admitted", input) }
	}
	if !Null(d).ValidResult(d.Usage()) { t.Fatal("typed NULL rejected") }
	cancelled, cancel := context.WithCancel(ctx); cancel()
	if _, err := d.Input(cancelled, "123456789X", -1); err != context.Canceled { t.Fatal(err) }
}

func TestIsbnFormatting(t *testing.T) {
	d, err := Isbn(60000, "isbn", "public.isbn"); if err != nil { t.Fatal(err) }
	for _, tc := range []struct{ input, output string }{
		{"0000000000", "0-00-000000-0"},
		{"9780200000000", "0-200-00000-4"},
		{"9780700000005", "0-7000-0000-3"},
		{"9780950000008", "0-9500000-0-0"},
		{"9791000000008", "979-10-00-00000-8"},
		{"9791234567896", "979-123456789-6"},
		{"978-0-393-04002-9", "0-393-04002-X"},
		{"1 234-56789X", "1-234-56789-X"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1); if err != nil { t.Fatal(tc.input, err) }
		if out, err := d.Output(context.Background(), v); err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
	}
}

func TestIsbnIdentity(t *testing.T) {
	d, err := Isbn(60000, "isbn", "public.isbn"); if err != nil { t.Fatal(err) }
	foreign, err := Isbn(d.ID(), "isbn", "public.isbn"); if err != nil { t.Fatal(err) }
	ean, err := Ean13(d.ID(), "isbn", "public.isbn"); if err != nil { t.Fatal(err) }
	v, err := d.Input(context.Background(), "123456789X", -1); if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "123456789X!", -1); if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, marked, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		if _, err := source.Retag(ean); err == nil { t.Fatal("sibling family retagged") }
		if _, err := source.RemapIsbn(ean); err == nil { t.Fatal("sibling family imported") }
		mapped, err := source.RemapIsbn(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() || mapped.integer != source.integer { t.Fatal(mapped, err) }
	}
	for _, packed := range []int64{-1, 0, 19999999999999, 19580246913570, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid packed family admitted", packed) }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid packed output admitted") }
	}
}
