package sqltype

import (
	"context"
	"testing"
)

func TestEan13Codec(t *testing.T) {
	d, err := Ean13(60000, "ean13", "public.ean13")
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	for _, tc := range []struct { input string; packed int64; output string }{
		{"0000000000000", 0, "000-000000000-0"},
		{"0123456789012", 246913578024, "012-345678901-2"},
		{"123456789012?", 2469135780256, "123-456789012-8"},
		{"1234567890120!", 2469135780257, "123-456789012-8!"},
		{"1234567890128!", 2469135780257, "123-456789012-8!"},
		{"123456789012?!", 2469135780256, "123-456789012-8"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil { t.Fatal(tc.input, err) }
		if v.integer != tc.packed || v.IsNull() || !v.ValidResult(d.Usage()) { t.Fatal(tc.input, v) }
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'" { t.Fatal(literal, err) }
	}
	if !Null(d).ValidResult(d.Usage()) { t.Fatal("typed NULL rejected") }
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(cancelled, "1234567890128", -1); err != context.Canceled { t.Fatal(err) }
}

func TestEan13Formatting(t *testing.T) {
	d, err := Ean13(60000, "ean13", "public.ean13")
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct { input, output string }{
		{"9780123456786", "978-0-12-345678-6"},
		{"9790123456785", "979-0-1234-5678-5"},
		{"9791234567896", "979-123456789-6"},
		{"9771234567898", "977-1234-567-89-8"},
		{"9780950000008", "978-0-9500000-0-8"},
		{"9791000000008", "979-10-00-00000-8"},
		{"1400000000007", "140000000000-7"},
	} {
		v, err := d.Input(context.Background(), tc.input, -1)
		if err != nil { t.Fatal(tc.input, err) }
		out, err := d.Output(context.Background(), v)
		if err != nil || out != tc.output { t.Fatal(tc.input, out, err) }
	}
}

func TestEan13Identity(t *testing.T) {
	d, err := Ean13(60000, "ean13", "public.ean13")
	if err != nil { t.Fatal(err) }
	foreign, err := Ean13(d.ID(), "ean13", "public.ean13")
	if err != nil { t.Fatal(err) }
	v, err := d.Input(context.Background(), "1234567890128", -1)
	if err != nil { t.Fatal(err) }
	marked, err := d.Input(context.Background(), "1234567890128!", -1)
	if err != nil { t.Fatal(err) }
	if SameDatum(v, marked) || !SameDatum(v, v) { t.Fatal("invalid marker identity") }
	for _, source := range []Value{v, Null(d)} {
		if _, err := source.Retag(foreign); err == nil { t.Fatal("foreign generation retagged") }
		mapped, err := source.RemapEan13(foreign)
		if err != nil || mapped.Type() != foreign || mapped.IsNull() != source.IsNull() { t.Fatal(mapped, err) }
	}
	for _, packed := range []int64{-1, 20000000000000} {
		bad := Value{typ: d, integer: packed}
		if bad.ValidResult(d.Usage()) || SameDatum(bad, bad) { t.Fatal("invalid packed value admitted") }
		if _, err := d.Output(context.Background(), bad); err == nil { t.Fatal("invalid packed output admitted") }
	}
	if _, err := v.RemapEan13(&Descriptor{id: Int8}); err == nil { t.Fatal("foreign family imported") }
}
