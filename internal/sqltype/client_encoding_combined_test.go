package sqltype

import (
	"context"
	"testing"
)

func clientPolicyCombined(t *testing.T) {
	// Both independent release direction tables were read: the vendored
	// utf8_to_euc_jis_2004.map ULmap and euc_jis_2004_to_utf8.map LUmap.
	// No expected bytes are obtained by running/inverting a converter.
	cases := []struct{ utf, local string }{
		{"c3a6cc80", "abc4"}, {"c994cc80", "abc8"}, {"c994cc81", "abc9"},
		{"c999cc80", "abcc"}, {"c999cc81", "abcd"}, {"c99acc80", "abce"}, {"c99acc81", "abcf"},
		{"ca8ccc80", "abca"}, {"ca8ccc81", "abcb"}, {"cba5cba9", "abe6"}, {"cba9cba5", "abe5"},
		{"e3818be3829a", "a4f7"}, {"e3818de3829a", "a4f8"}, {"e3818fe3829a", "a4f9"}, {"e38191e3829a", "a4fa"}, {"e38193e3829a", "a4fb"},
		{"e382abe3829a", "a5f7"}, {"e382ade3829a", "a5f8"}, {"e382afe3829a", "a5f9"}, {"e382b1e3829a", "a5fa"}, {"e382b3e3829a", "a5fb"}, {"e382bbe3829a", "a5fc"}, {"e38384e3829a", "a5fd"}, {"e38388e3829a", "a5fe"}, {"e387b7e3829a", "a6f8"},
	}
	p := clientPolicy(t, "EUC_JIS_2004")
	if len(cases) != 25 {
		t.Fatal("combined inventory", len(cases))
	}
	for _, w := range cases {
		utf, local := binaryEncodingHex(t, w.utf), binaryEncodingHex(t, w.local)
		out, err := p.Encode(context.Background(), utf)
		if err != nil || out != local {
			t.Fatalf("combined output %x want %x %v", out, local, err)
		}
		out, err = p.Decode(context.Background(), local)
		if err != nil || out != utf {
			t.Fatalf("combined input %x want %x %v", out, utf, err)
		}
	}
}
