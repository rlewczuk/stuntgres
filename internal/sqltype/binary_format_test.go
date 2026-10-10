package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBinaryFormatVectors(t *testing.T) {
	for _, tc := range []struct{ kind, raw, encoded string }{
		{"hex", "\x00\x01\x7f\x80\xff", "00017f80ff"},
		{"escape", "\x00\x01\x1f \\\x7f\x80\xff", "\\000\x01\x1f \\\\\x7f\\200\\377"},
		{"base64", "123\x00\x01", "MTIzAAE="},
		{"base64", "f", "Zg=="}, {"base64", "fo", "Zm8="}, {"base64", "foo", "Zm9v"},
	} {
		got, err := binaryFormat(context.Background(), tc.raw, tc.kind, true)
		if err != nil || got != tc.encoded {
			t.Fatalf("%s encode %q: %q %v", tc.kind, tc.raw, got, err)
		}
		got, err = binaryFormat(context.Background(), tc.encoded, tc.kind, false)
		if err != nil || got != tc.raw {
			t.Fatalf("%s decode: %q %v", tc.kind, got, err)
		}
	}
	for _, tc := range []struct{ raw, want string }{{"AA=A", "\x00"}, {"AA==AAAA", "\x00\x00"}, {"AB==", "\x00"}, {"AAA=AAAA", "\x00\x00\x00\x00"}} {
		got, err := binaryFormat(context.Background(), tc.raw, "base64", false)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, n := range []int{55, 56, 57, 58, 113, 114, 115} {
		// A triple of zeros is AAAA; padded suffix has no newline even at column76.
		want := strings.Repeat(strings.Repeat("AAAA", 19)+"\n", n/57) + strings.Repeat("AAAA", (n%57)/3)
		if n%3 == 1 {
			want += "AA=="
		}
		if n%3 == 2 {
			want += "AAA="
		}
		got, err := binaryFormat(context.Background(), strings.Repeat("\x00", n), "base64", true)
		if err != nil || got != want {
			t.Fatal(n, got, want, err)
		}
	}
}

func TestBinaryFormatDiagnostics(t *testing.T) {
	for _, tc := range []struct{ raw, kind, code, message, hint string }{
		{"a", " hex", "22023", `unrecognized encoding: " hex"`, ""},
		{"a", "hex", "22023", "invalid hexadecimal data: odd number of digits", ""},
		{"0 é", "hex", "22023", `invalid hexadecimal digit: " "`, ""},
		{"0界", "hex", "22023", `invalid hexadecimal digit: "界"`, ""},
		{"\\400", "escape", "22P02", "invalid input syntax for type bytea", ""},
		{"=AAA", "base64", "22023", `unexpected "=" while decoding base64 sequence`, ""},
		{"AAé", "base64", "22023", `invalid symbol "é" found while decoding base64 sequence`, ""},
		{"AAA", "base64", "22023", "invalid base64 end sequence", "Input data is missing padding, is truncated, or is otherwise corrupted."},
	} {
		_, err := binaryFormat(context.Background(), tc.raw, tc.kind, false)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%q/%s: %#v want %#v", tc.raw, tc.kind, err, want)
		}
	}
	for _, encode := range []bool{false, true} {
		if err := binaryFormatSize(binaryFormatLimit, encode); err != nil {
			t.Fatal(err)
		}
		var e *sqlstate.Error
		if !errors.As(binaryFormatSize(binaryFormatLimit+1, encode), &e) || e.Code != "54000" {
			t.Fatal(e)
		}
	}
}

func TestBinaryFormatCancellation(t *testing.T) {
	for _, kind := range []string{"hex", "escape", "base64"} {
		raw := strings.Repeat("A", 32768)
		encoded, err := binaryFormat(context.Background(), raw, kind, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, encode := range []bool{true, false} {
			input := raw
			if !encode {
				input = encoded
			}
			for _, point := range []int{1, 3, 7, 12} {
				ctx := &mathCheckpointContext{Context: context.Background(), remaining: point}
				got, err := binaryFormat(ctx, input, kind, encode)
				if !errors.Is(err, context.Canceled) || got != "" {
					t.Fatal(kind, encode, point, len(got), err)
				}
			}
		}
	}
}

func TestBinaryFormatAllocation(t *testing.T) {
	raw := strings.Repeat("A", 32768)
	ctx := context.Background()
	var sink string
	count := testing.AllocsPerRun(10, func() {
		var err error
		sink, err = binaryFormat(ctx, raw, "hex", true)
		if err != nil {
			panic(err)
		}
	})
	if count > 2 || len(sink) != 65536 {
		t.Fatal(count, len(sink))
	}
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			for range 16 {
				var err error
				sink, err = binaryFormat(ctx, raw, "hex", true)
				if err != nil {
					panic(err)
				}
			}
		}
	})
	if bytes := result.AllocedBytesPerOp() / 16; bytes > 65536+1024 {
		t.Fatal("payload-sized scratch allocation", bytes)
	}
}
