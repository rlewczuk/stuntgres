package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/pgencoding18"
	"stuntgres/internal/util"
)

func clientPolicy(t *testing.T, name string) ClientEncodingPolicy {
	t.Helper()
	id := EncodingID(name)
	in, _ := RegisteredEncodingConversion(pgencoding18.ConversionRoutine(id, 6))
	out, _ := RegisteredEncodingConversion(pgencoding18.ConversionRoutine(6, id))
	p, err := NewClientEncodingPolicy(id, in, out)
	if err != nil {
		t.Fatal(name, err)
	}
	return p
}

func TestClientEncodingProviderPairs(t *testing.T) {
	// Independent release/manual literals, not kernel-generated expectations.
	spokes := []struct{ name, local, utf string }{
		{"EUC_JP", "a1a1", "e38080"}, {"EUC_CN", "a1a1", "e38080"}, {"EUC_KR", "a1a1", "e38080"}, {"EUC_TW", "a1a1", "e38080"}, {"EUC_JIS_2004", "a1a1", "e38080"},
		{"LATIN1", "a0", "c2a0"}, {"LATIN2", "a0", "c2a0"}, {"LATIN3", "a0", "c2a0"}, {"LATIN4", "a0", "c2a0"}, {"LATIN5", "a0", "c2a0"}, {"LATIN6", "a0", "c2a0"}, {"LATIN7", "a0", "c2a0"}, {"LATIN8", "a0", "c2a0"}, {"LATIN9", "a0", "c2a0"}, {"LATIN10", "a0", "c2a0"},
		{"WIN1256", "c7", "d8a7"}, {"WIN1258", "a0", "c2a0"}, {"WIN866", "a0", "d0b0"}, {"WIN874", "a1", "e0b881"}, {"KOI8R", "c1", "d0b0"}, {"WIN1251", "e0", "d0b0"}, {"WIN1252", "a0", "c2a0"},
		{"ISO_8859_5", "d0", "d0b0"}, {"ISO_8859_6", "a0", "c2a0"}, {"ISO_8859_7", "a0", "c2a0"}, {"ISO_8859_8", "a0", "c2a0"},
		{"WIN1250", "a0", "c2a0"}, {"WIN1253", "e1", "ceb1"}, {"WIN1254", "a0", "c2a0"}, {"WIN1255", "e0", "d790"}, {"WIN1257", "a0", "c2a0"}, {"KOI8U", "c1", "d0b0"},
		{"SJIS", "8140", "e38080"}, {"BIG5", "a140", "e38080"}, {"GBK", "a1a1", "e38080"}, {"UHC", "a1a1", "e38080"}, {"GB18030", "a1a1", "e38080"}, {"JOHAB", "d9e6", "e282ac"}, {"SHIFT_JIS_2004", "8140", "e38080"},
	}
	if len(spokes) != 39 {
		t.Fatal("incomplete independent spoke inventory", len(spokes))
	}
	for _, w := range spokes {
		t.Run(w.name, func(t *testing.T) {
			p := clientPolicy(t, w.name)
			local, utf := binaryEncodingHex(t, w.local), binaryEncodingHex(t, w.utf)
			got, err := p.Decode(context.Background(), local)
			if err != nil || got != utf {
				t.Fatalf("input %x want %x: %v", got, utf, err)
			}
			got, err = p.Encode(context.Background(), utf)
			if err != nil || got != local {
				t.Fatalf("output %x want %x: %v", got, local, err)
			}
			prefix := []byte("prefix:")
			gotBytes, err := p.Append(context.Background(), prefix, utf)
			if err != nil || string(gotBytes) != "prefix:"+local {
				t.Fatalf("append %x %v", gotBytes, err)
			}
		})
	}
	for _, name := range []string{"UTF8", "SQL_ASCII"} {
		p := clientPolicy(t, name)
		raw := "é🙂"
		got, err := p.Decode(context.Background(), raw)
		if err != nil || got != raw {
			t.Fatal(got, err)
		}
		got, err = p.Encode(context.Background(), raw)
		if err != nil || got != raw {
			t.Fatal(got, err)
		}
		_, err = p.Decode(context.Background(), "\xff")
		clientPolicyDiagnostic(t, err, "22021", `invalid byte sequence for encoding "UTF8": 0xff`)
	}
	_, err := NewClientEncodingPolicy(EncodingID("MULE_INTERNAL"), EncodingConversionHandler{}, EncodingConversionHandler{})
	clientPolicyDiagnostic(t, err, "0A000", "conversion between MULE_INTERNAL and UTF8 is not supported")
	for _, w := range []struct{ name, raw, code, message string }{
		{"SJIS", "\x82", "22021", `invalid byte sequence for encoding "SJIS": 0x82`},
		{"GB18030", "\x84\x30\x9c", "22021", `invalid byte sequence for encoding "GB18030": 0x84 0x30 0x9c`},
		{"GB18030", "\x84\x31\xa5\x30", "22P05", `character with byte sequence 0x84 0x31 0xa5 0x30 in encoding "GB18030" has no equivalent in encoding "UTF8"`},
		{"WIN1252", "\x81", "22P05", `character with byte sequence 0x81 in encoding "WIN1252" has no equivalent in encoding "UTF8"`},
		{"LATIN1", "\x00", "22021", `invalid byte sequence for encoding "LATIN1": 0x00`},
	} {
		got, err := clientPolicy(t, w.name).Decode(context.Background(), w.raw)
		if got != "" {
			t.Fatalf("failed datum published %x", got)
		}
		clientPolicyDiagnostic(t, err, w.code, w.message)
	}
	_, err = clientPolicy(t, "LATIN1").Encode(context.Background(), "€")
	clientPolicyDiagnostic(t, err, "22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`)
	clientPolicyCombined(t)
}

func clientPolicyDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Detail != "" || d.Hint != "" || d.Context != "" || d.Position != "" {
		t.Fatalf("diagnostic %+v want %s %q", d, code, message)
	}
}

func TestClientEncodingOwnership(t *testing.T) {
	p := clientPolicy(t, "LATIN1")
	old, err := p.Encode(context.Background(), strings.Repeat("é", 100))
	if err != nil {
		t.Fatal(err)
	}
	backing := unsafe.StringData(old)
	for range 4 {
		if _, err := p.Encode(context.Background(), strings.Repeat("ö", 10000)); err != nil {
			t.Fatal(err)
		}
	}
	if old != strings.Repeat("\xe9", 100) || unsafe.StringData(old) != backing {
		t.Fatal("accepted output backing mutated")
	}
	for _, name := range []string{"LATIN1", "SJIS", "GB18030", "EUC_JIS_2004"} {
		p := clientPolicy(t, name)
		local, unicode := "\xe9", "é"
		if name == "SJIS" {
			local, unicode = "\x82\xa0", "あ"
		}
		if name == "GB18030" {
			local, unicode = "\x84\x30\x9c\x38", "飯"
		}
		if name == "EUC_JIS_2004" {
			local, unicode = "\xa5\xf7", "カ゚"
		}
		for _, outbound := range []bool{false, true} {
			raw := strings.Repeat(local, 10000)
			if outbound {
				raw = strings.Repeat(unicode, 10000)
			}
			polls := (len(raw) + 4095) / 4096
			// The second-or-later poll of each actual validation/count/emission
			// loop, never a scheduler delay or merely an admission checkpoint.
			checkpoints := []int{3, polls + 5, 2*polls + 6}
			for _, at := range checkpoints {
				ctx := &binaryEncodingCancelContext{Context: context.Background(), cancelAt: at}
				var output string
				var err error
				if outbound {
					output, err = p.Encode(ctx, raw)
				} else {
					output, err = p.Decode(ctx, raw)
				}
				if !errors.Is(err, context.Canceled) || output != "" {
					t.Fatalf("%s outbound%t poll%d: %d bytes %v", name, outbound, at, len(output), err)
				}
				if outbound {
					ctx = &binaryEncodingCancelContext{Context: context.Background(), cancelAt: at}
					out, err := p.Append(ctx, []byte("owned"), raw)
					if !errors.Is(err, context.Canceled) || string(out) != "owned" {
						t.Fatalf("partial append %x %v", out, err)
					}
				}
			}
		}
	}
	for _, name := range []string{"UTF8", "SQL_ASCII"} {
		p := clientPolicy(t, name)
		allocations, bytes := util.AllocationsPerCall(10, func() {
			if _, err := p.Encode(context.Background(), "é🙂"); err != nil {
				panic(err)
			}
		})
		if allocations != 0 || bytes != 0 {
			t.Fatalf("identity allocated %s %d/%d", name, allocations, bytes)
		}
	}
	if binaryEncodingInputSize(int(^uint(0)>>1) / 4) {
		t.Fatal("conversion size guard")
	}
}
