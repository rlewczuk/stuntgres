package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/pgencoding18"
)

func TestConversionHandlerReleasedPairs(t *testing.T) {
	ctx := context.Background()
	pairs, handlers := 0, map[string]bool{}
	for source := range 42 {
		for destination := range 42 {
			name := pgencoding18.ConversionRoutine(source, destination)
			if name == "" {
				if pgencoding18.HasPair(source, destination) {
					t.Fatal("registered pair lacks a routine", source, destination)
				}
				continue
			}
			pairs++
			handlers[name] = true
			handler, ok := RegisteredEncodingConversion(name)
			if !ok || !handler.Supports(source, destination) {
				t.Fatal("released handler unavailable", name, source, destination)
			}
			for _, input := range []string{"", "A", "ASCII"} {
				output, consumed, err := handler.Convert(ctx, input, source, destination)
				if err != nil || output != input || consumed != len(input) {
					t.Fatal(name, source, destination, output, consumed, err)
				}
			}
		}
	}
	if pairs != 128 || len(handlers) != 84 {
		t.Fatal("released capability cardinality", pairs, len(handlers))
	}
	if _, ok := RegisteredEncodingConversion("utf8_to_iso_8859_1"); ok {
		t.Fatal("conversion object name was treated as a routine")
	}
}

func TestConversionHandlerRealDiagnostics(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		routine, input      string
		source, destination int
		code, message       string
	}{
		{"utf8_to_iso8859_1", "", 8, 6, "XX000", `expected source encoding "UTF8", but got "LATIN1"`},
		{"utf8_to_iso8859_1", "", 6, 9, "XX000", `expected destination encoding "LATIN1", but got "LATIN2"`},
		{"utf8_to_iso8859_1", "\xff", 6, 8, "22021", `invalid byte sequence for encoding "UTF8": 0xff`},
		{"utf8_to_iso8859_1", "€", 6, 8, "22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`},
		{"utf8_to_win", "", 6, 8, "XX000", "unexpected encoding ID 8 for WIN character sets"},
		{"win_to_utf8", "", 8, 6, "XX000", "unexpected encoding ID 8 for WIN character sets"},
		{"utf8_to_iso8859", "", 6, 8, "XX000", "unexpected encoding ID 8 for ISO 8859 character sets"},
		{"iso8859_to_utf8", "", 8, 6, "XX000", "unexpected encoding ID 8 for ISO 8859 character sets"},
	} {
		handler, ok := RegisteredEncodingConversion(c.routine)
		if !ok {
			t.Fatal(c.routine)
		}
		output, consumed, err := handler.Convert(ctx, c.input, c.source, c.destination)
		var diagnostic *sqlstate.Error
		if output != "" || consumed != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != c.code || diagnostic.Message != c.message || diagnostic.Detail != "" || diagnostic.Hint != "" || diagnostic.Position != "" {
			t.Fatal(c, output, consumed, err)
		}
	}
	for _, c := range []struct {
		routine, input, output string
		source, destination    int
	}{
		{"utf8_to_iso8859_1", "é", "\xe9", 6, 8}, {"iso8859_1_to_utf8", "\xe9", "é", 8, 6}, {"utf8_to_win", "€", "\x80", 6, 24},
	} {
		handler, _ := RegisteredEncodingConversion(c.routine)
		output, consumed, err := handler.Convert(ctx, c.input, c.source, c.destination)
		if err != nil || output != c.output || consumed != len(c.input) {
			t.Fatal(c, output, consumed, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	handler, _ := RegisteredEncodingConversion("utf8_to_iso8859_1")
	if _, consumed, err := handler.Convert(canceled, "", 6, 8); !errors.Is(err, context.Canceled) || consumed != 0 {
		t.Fatal("entry cancellation", consumed, err)
	}
}

func TestConversionHandlerRawEncodingGate(t *testing.T) {
	if EncodingID("u_t-f8") != 6 || EncodingID("ISO-8859-1") != 8 || EncodingName(7) != "MULE_INTERNAL" {
		t.Fatal("encoding identity disagreement")
	}
	long := "UTF8" + strings.Repeat("-", 60)
	if EncodingID(long) != -1 || pgencoding18.ID(clipName(long)) != 6 {
		t.Fatal("CREATE Sconst gate confused with NAME clipping")
	}
	_, _, unchanged, err := PrepareBinaryEncoding(context.Background(), "", "UTF8", "LATIN1")
	if err != nil || !unchanged {
		t.Fatal("empty input demanded a default", err)
	}
	if _, _, _, err = PrepareBinaryEncoding(context.Background(), "\xff", "UTF8", "LATIN1"); err == nil {
		t.Fatal("invalid entire source skipped validation")
	}
}
