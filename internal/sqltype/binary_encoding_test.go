package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/pgencoding18"
	"testing"
)

func binaryEncodingHex(t *testing.T, s string) string {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestBinaryEncodingPairs(t *testing.T) {
	// Independent release/manual literals, also pinned by public reference goldens.
	spokes := []struct{ name, local, utf string }{
		{"EUC_JP", "a1a1", "e38080"}, {"EUC_CN", "a1a1", "e38080"}, {"EUC_KR", "a1a1", "e38080"}, {"EUC_TW", "a1a1", "e38080"}, {"EUC_JIS_2004", "a1a1", "e38080"},
		{"LATIN1", "a0", "c2a0"}, {"LATIN2", "a0", "c2a0"}, {"LATIN3", "a0", "c2a0"}, {"LATIN4", "a0", "c2a0"}, {"LATIN5", "a0", "c2a0"}, {"LATIN6", "a0", "c2a0"}, {"LATIN7", "a0", "c2a0"}, {"LATIN8", "a0", "c2a0"}, {"LATIN9", "a0", "c2a0"}, {"LATIN10", "a0", "c2a0"},
		{"WIN1256", "c7", "d8a7"}, {"WIN1258", "a0", "c2a0"}, {"WIN866", "a0", "d0b0"}, {"WIN874", "a1", "e0b881"}, {"KOI8R", "c1", "d0b0"}, {"WIN1251", "e0", "d0b0"}, {"WIN1252", "a0", "c2a0"},
		{"ISO_8859_5", "d0", "d0b0"}, {"ISO_8859_6", "a0", "c2a0"}, {"ISO_8859_7", "a0", "c2a0"}, {"ISO_8859_8", "a0", "c2a0"},
		{"WIN1250", "a0", "c2a0"}, {"WIN1253", "e1", "ceb1"}, {"WIN1254", "a0", "c2a0"}, {"WIN1255", "e0", "d790"}, {"WIN1257", "a0", "c2a0"}, {"KOI8U", "c1", "d0b0"},
		{"SJIS", "8140", "e38080"}, {"BIG5", "a140", "e38080"}, {"GBK", "a1a1", "e38080"}, {"UHC", "a1a1", "e38080"}, {"GB18030", "a1a1", "e38080"}, {"JOHAB", "d9e6", "e282ac"}, {"SHIFT_JIS_2004", "8140", "e38080"},
	}
	type pair struct{ src, dst, in, out string }
	var pairs []pair
	for _, w := range spokes {
		pairs = append(pairs, pair{w.name, "UTF8", w.local, w.utf}, pair{"UTF8", w.name, w.utf, w.local})
	}
	for _, w := range []struct{ name, local, mic string }{
		{"KOI8R", "c1", "8bc1"}, {"ISO_8859_5", "d0", "8bc1"}, {"WIN1251", "e0", "8bc1"}, {"WIN866", "a0", "8bc1"},
		{"EUC_CN", "a1a1", "91a1a1"}, {"EUC_JP", "a1a1", "92a1a1"}, {"SJIS", "8140", "92a1a1"}, {"EUC_KR", "a1a1", "93a1a1"}, {"EUC_TW", "a1a1", "95a1a1"}, {"BIG5", "a140", "95a1a1"},
		{"LATIN2", "a0", "82a0"}, {"WIN1250", "a0", "82a0"}, {"LATIN1", "a0", "81a0"}, {"LATIN3", "a0", "83a0"}, {"LATIN4", "a0", "84a0"},
	} {
		pairs = append(pairs, pair{w.name, "MULE_INTERNAL", w.local, w.mic}, pair{"MULE_INTERNAL", w.name, w.mic, w.local})
	}
	cyr := []struct{ name, raw string }{{"KOI8R", "c1"}, {"ISO_8859_5", "d0"}, {"WIN1251", "e0"}, {"WIN866", "a0"}}
	for _, a := range cyr {
		for _, b := range cyr {
			if a.name != b.name {
				pairs = append(pairs, pair{a.name, b.name, a.raw, b.raw})
			}
		}
	}
	for _, p := range []pair{{"LATIN2", "WIN1250", "a0", "a0"}, {"EUC_JP", "SJIS", "a1a1", "8140"}, {"EUC_TW", "BIG5", "a1a1", "a140"}, {"EUC_JIS_2004", "SHIFT_JIS_2004", "a1a1", "8140"}} {
		pairs = append(pairs, p, pair{p.dst, p.src, p.out, p.in})
	}
	seen := map[[2]int]bool{}
	for _, p := range pairs {
		key := [2]int{pgencoding18.ID(p.src), pgencoding18.ID(p.dst)}
		if seen[key] {
			t.Fatal("duplicate pair", key)
		}
		seen[key] = true
		t.Run(p.src+"_"+p.dst, func(t *testing.T) {
			got, e := ConvertBinaryEncoding(context.Background(), binaryEncodingHex(t, p.in), p.src, p.dst)
			if e != nil || got != binaryEncodingHex(t, p.out) {
				t.Fatalf("%x %v want %s", got, e, p.out)
			}
		})
	}
	if len(seen) != 128 {
		t.Fatal("pair count", len(seen))
	}
	for a := range 42 {
		for b := range 42 {
			if pgencoding18.HasPair(a, b) != seen[[2]int{a, b}] {
				t.Fatal("unexpected registry pair", a, b)
			}
		}
	}
	identities := append(spokes, struct{ name, local, utf string }{"SQL_ASCII", "ff", ""}, struct{ name, local, utf string }{"UTF8", "c3a9", ""}, struct{ name, local, utf string }{"MULE_INTERNAL", "81a0", ""})
	if len(identities) != 42 {
		t.Fatal("identity count")
	}
	for _, w := range identities {
		t.Run(w.name+"_identity", func(t *testing.T) {
			raw := binaryEncodingHex(t, w.local)
			for _, s := range []string{raw, ""} {
				got, e := ConvertBinaryEncoding(context.Background(), s, w.name, w.name)
				if e != nil || got != s {
					t.Fatal(got, e)
				}
			}
			_, e := ConvertBinaryEncoding(context.Background(), "\x00", w.name, w.name)
			stringEncodingUnitDiagnostic(t, e, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: fmt.Sprintf("invalid byte sequence for encoding %q: 0x00", w.name)})
		})
	}
}
func TestBinaryEncodingDiagnostics(t *testing.T) {
	for _, tc := range []struct{ raw, src, dst, code, message string }{
		{"\xff", "nope", "also nope", "22023", `invalid source encoding name "nope"`},
		{"\xff", "UTF8", "nope", "22023", `invalid destination encoding name "nope"`},
		{"a", "bad\"name", "UTF8", "22023", `invalid source encoding name "bad"name"`},
		{"€\xff", "UTF8", "LATIN1", "22021", `invalid byte sequence for encoding "UTF8": 0xff`},
		{"\xa1", "EUC_JP", "LATIN1", "22021", `invalid byte sequence for encoding "EUC_JP": 0xa1`},
		{"a", "LATIN1", "LATIN2", "42883", `default conversion function for encoding "LATIN1" to "LATIN2" does not exist`},
		{"\x81", "WIN1252", "UTF8", "22P05", `character with byte sequence 0x81 in encoding "WIN1252" has no equivalent in encoding "UTF8"`},
		{"\xb0", "WIN866", "WIN1251", "22P05", `character with byte sequence 0xb0 in encoding "WIN866" has no equivalent in encoding "WIN1251"`},
		{"\x82\xa0", "MULE_INTERNAL", "LATIN1", "22P05", `character with byte sequence 0x82 0xa0 in encoding "MULE_INTERNAL" has no equivalent in encoding "LATIN1"`},
		{"\x8e\xa3\xa1\xa1", "EUC_TW", "BIG5", "22P05", `character with byte sequence 0x8e 0xa3 0xa1 0xa1 in encoding "EUC_TW" has no equivalent in encoding "BIG5"`},
	} {
		_, e := ConvertBinaryEncoding(context.Background(), tc.raw, tc.src, tc.dst)
		stringEncodingUnitDiagnostic(t, e, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message})
	}
	for _, tc := range []struct{ raw, src, dst string }{{"\x81", "WIN1252", "WIN1252"}, {"é", "SQL_ASCII", "UTF8"}, {"\xff", "SQL_ASCII", "LATIN1"}, {"é", "UTF8", "SQL_ASCII"}, {"", "LATIN1", "LATIN2"}} {
		got, e := ConvertBinaryEncoding(context.Background(), tc.raw, tc.src, tc.dst)
		if e != nil || got != tc.raw {
			t.Fatal(tc, got, e)
		}
	}
	_, e := ConvertBinaryEncoding(context.Background(), "\xff", "SQL_ASCII", "UTF8")
	stringEncodingUnitDiagnostic(t, e, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0xff`})
	for _, tc := range []struct{ alias, in, out string }{{"unicode", "c3a9", "c3a9"}, {"windows950", "a140", "e38080"}, {"mskanji", "8140", "e38080"}, {"ABC", "a0", "c2a0"}} {
		got, e := ConvertBinaryEncoding(context.Background(), binaryEncodingHex(t, tc.in), tc.alias, "UTF8")
		if e != nil || got != binaryEncodingHex(t, tc.out) {
			t.Fatal(tc, got, e)
		}
	}
}

type binaryEncodingCancelContext struct {
	context.Context
	calls, cancelAt int
}

func (c *binaryEncodingCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func TestBinaryEncodingCancellation(t *testing.T) {
	raw := strings.Repeat("é", 10000)
	// Entry, then five bounded polls plus a final poll in each of three passes.
	for cancelAt := 1; cancelAt <= 19; cancelAt++ {
		ctx := &binaryEncodingCancelContext{Context: context.Background(), cancelAt: cancelAt}
		got, e := ConvertBinaryEncoding(ctx, raw, "UTF8", "LATIN1")
		if !errors.Is(e, context.Canceled) || got != "" {
			t.Fatalf("phase %d: %d calls %d bytes %v", cancelAt, ctx.calls, len(got), e)
		}
	}
	for _, tc := range []struct{ raw, source, destination string }{{"\x81\x40", "SJIS", "EUC_JP"}, {"\xa1\x40", "BIG5", "MULE_INTERNAL"}, {"\xa1\xa1", "EUC_JIS_2004", "SHIFT_JIS_2004"}, {"\xc4\x80", "UTF8", "GB18030"}} {
		for _, cancelAt := range []int{3, 8, 14} {
			ctx := &binaryEncodingCancelContext{Context: context.Background(), cancelAt: cancelAt}
			got, e := ConvertBinaryEncoding(ctx, strings.Repeat(tc.raw, 10000), tc.source, tc.destination)
			if got != "" || !errors.Is(e, context.Canceled) {
				t.Fatal("algorithm cancellation", tc.source, cancelAt, len(got), e)
			}
		}
	}
	for _, ids := range [][2]string{{"UTF8", "UTF8"}, {"UTF8", "SQL_ASCII"}, {"SQL_ASCII", "UTF8"}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e := ConvertBinaryEncoding(ctx, raw, ids[0], ids[1]); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
}
func TestBinaryEncodingOwnershipAndSizes(t *testing.T) {
	raw := strings.Repeat("é", 100)
	for _, ids := range [][2]string{{"UTF8", "UTF8"}, {"UTF8", "SQL_ASCII"}, {"SQL_ASCII", "UTF8"}} {
		if a := testing.AllocsPerRun(100, func() {
			got, e := ConvertBinaryEncoding(context.Background(), raw, ids[0], ids[1])
			if e != nil || got != raw {
				panic("identity")
			}
		}); a != 0 {
			t.Fatal("identity allocations", ids, a)
		}
	}
	max := int(^uint(0)>>1) / 4
	if !binaryEncodingInputSize(max-1) || binaryEncodingInputSize(max) {
		t.Fatal("release huge-size guard")
	}
	stringEncodingUnitDiagnostic(t, binaryEncodingSizeError(123), &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "out of memory", Detail: "String of 123 bytes is too long for encoding conversion."})
}
