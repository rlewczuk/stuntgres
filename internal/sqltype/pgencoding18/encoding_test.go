package pgencoding18

import (
	"encoding/hex"
	"testing"
)

func rawHex(t *testing.T, s string) string {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestBinaryEncodingVerifiers(t *testing.T) {
	for _, tc := range []struct {
		id           int
		raw          string
		valid, width int
	}{
		{6, "c2", -1, 2}, {6, "c220", -1, 2}, {6, "c080", -1, 2}, {6, "eda080", -1, 3}, {6, "f4908080", -1, 4}, {6, "e282", -1, 3}, {6, "ff", -1, 1},
		{1, "8fa1", -1, 3}, {1, "a120", -1, 2}, {1, "8e80", -1, 2}, {2, "a120", -1, 2}, {3, "a120", -1, 2}, {4, "8ea2a1", -1, 4}, {4, "a120", -1, 2}, {5, "8fa120", -1, 3},
		{7, "81", -1, 2}, {7, "8120", -1, 2}, {7, "92a1", -1, 3}, {7, "9cf0a1", -1, 4}, {7, "80", 1, 1}, {7, "8eff", 1, 1}, {7, "9ba1a1", 3, 3}, {7, "9df6a1a1", 4, 4},
		{35, "817f", -1, 2}, {41, "817f", -1, 2}, {35, "a1", 1, 1}, {35, "df", 1, 1}, {35, "80a1", -1, 2},
		{36, "a1", -1, 2}, {37, "81", -1, 2}, {38, "81", -1, 2}, {36, "a120", 2, 2}, {37, "8120", 2, 2}, {38, "8120", 2, 2}, {36, "8d20", -1, 2}, {37, "8d20", -1, 2}, {38, "8d20", -1, 2},
		{39, "813081", -1, 4}, {39, "81302030", -1, 4}, {39, "81308130", 4, 4}, {39, "8140", 2, 2}, {39, "817f", -1, 2}, {40, "8120", -1, 2}, {40, "8fa1a1", 3, 3},
		{4, "80a1", 2, 2}, {4, "8ea7fefe", 4, 4}, {4, "8ea8a1a1", -1, 4}, {4, "8fa1a1", -1, 3},
	} {
		s := rawHex(t, tc.raw)
		if got := Verify(tc.id, s); got != tc.valid {
			t.Fatalf("verify id%d %s = %d want %d", tc.id, tc.raw, got, tc.valid)
		}
		if got := Length(tc.id, s); got != tc.width {
			t.Fatalf("width id%d %s = %d want%d", tc.id, tc.raw, got, tc.width)
		}
	}
	for id := range 42 {
		if Verify(id, "\x00") != -1 || Verify(id, "A") != 1 {
			t.Fatal("ASCII/NUL", id)
		}
	}
	for id := 8; id < 35; id++ {
		for b := 128; b < 256; b++ {
			if Verify(id, string([]byte{byte(b)})) != 1 {
				t.Fatal("single byte", id, b)
			}
		}
	}
}
func TestBinaryEncodingRadixBoundaries(t *testing.T) {
	r := &win1252_to_unicode_tree
	for _, tc := range []struct {
		code uint32
		n    int
		want uint32
	}{{0x7f, 1, 0}, {0x80, 1, 0xe282ac}, {0x81, 1, 0}, {0xff, 1, 0xc3bf}, {0x100, 2, 0}} {
		if got := r.lookup(tc.code, tc.n); got != tc.want {
			t.Fatalf("lookup %x=%x want%x", tc.code, got, tc.want)
		}
	}
	for _, tc := range []struct {
		code uint32
		want uint32
	}{{0xc280, 0}, {0xe282ac, 0x80}, {0xc3bf, 0xff}, {0xf0908080, 0}} {
		if got := win1252_from_unicode_tree.lookup(tc.code, Width(tc.code)); got != tc.want {
			t.Fatalf("reverse lookup %x=%x", tc.code, got)
		}
	}
}
func TestBinaryEncodingCombined(t *testing.T) {
	vectors := []struct{ euc, sjis, utf string }{
		{"a4f7", "82f5", "e3818be3829a"}, {"a4f8", "82f6", "e3818de3829a"}, {"a4f9", "82f7", "e3818fe3829a"}, {"a4fa", "82f8", "e38191e3829a"}, {"a4fb", "82f9", "e38193e3829a"},
		{"a5f7", "8397", "e382abe3829a"}, {"a5f8", "8398", "e382ade3829a"}, {"a5f9", "8399", "e382afe3829a"}, {"a5fa", "839a", "e382b1e3829a"}, {"a5fb", "839b", "e382b3e3829a"}, {"a5fc", "839c", "e382bbe3829a"}, {"a5fd", "839d", "e38384e3829a"}, {"a5fe", "839e", "e38388e3829a"}, {"a6f8", "83f6", "e387b7e3829a"},
		{"abc4", "8663", "c3a6cc80"}, {"abc8", "8667", "c994cc80"}, {"abc9", "8668", "c994cc81"}, {"abca", "8669", "ca8ccc80"}, {"abcb", "866a", "ca8ccc81"}, {"abcc", "866b", "c999cc80"}, {"abcd", "866c", "c999cc81"}, {"abce", "866d", "c99acc80"}, {"abcf", "866e", "c99acc81"}, {"abe5", "8685", "cba9cba5"}, {"abe6", "8686", "cba5cba9"},
	}
	for _, v := range vectors {
		for _, local := range []struct {
			id  int
			raw string
		}{{5, v.euc}, {41, v.sjis}} {
			in, utf := rawHex(t, local.raw), rawHex(t, v.utf)
			n := Length(6, utf)
			a, b, consumed, bad := Step(local.id, 6, in)
			if bad || consumed != len(in) || a != Pack(utf, n) || b != Pack(utf[n:], len(utf)-n) {
				t.Fatalf("combined %v: %x %x %d %v", local, a, b, consumed, bad)
			}
			a, b, consumed, bad = Step(6, local.id, utf)
			if bad || consumed != len(utf) || a != Pack(in, len(in)) || b != 0 {
				t.Fatalf("reverse combined %v: %x %x %d %v", local, a, b, consumed, bad)
			}
		}
	}
	a, b, n, bad := Step(6, 5, "かA")
	if a != 0xa4ab || b != 0 || n != 3 || bad {
		t.Fatal("combined fallback", a, b, n, bad)
	}
	a, b, n, bad = Step(6, 5, "かか")
	if a != 0xa4ab || b != 0 || n != 3 || bad {
		t.Fatal("noncombined lookahead", a, b, n, bad)
	}
}
func TestBinaryEncodingGB18030Ranges(t *testing.T) {
	// Independently transcribed inclusive release ranges. Check each endpoint
	// and the gaps on either side without consulting generated radix tables.
	vectors := [][4]uint32{
		{0x452, 0x200f, 0x8130d330, 0x8136a531}, {0x2643, 0x2e80, 0x8137a839, 0x8138fd38}, {0x361b, 0x3917, 0x8230a633, 0x8230f237},
		{0x3ce1, 0x4055, 0x8231d438, 0x8232af32}, {0x4160, 0x4336, 0x8232c937, 0x8232f837}, {0x44d7, 0x464b, 0x8233a339, 0x8233c931},
		{0x478e, 0x4946, 0x8233e838, 0x82349638}, {0x49b8, 0x4c76, 0x8234a131, 0x8234e733}, {0x9fa6, 0xd7ff, 0x82358f33, 0x8336c738},
		{0xe865, 0xf92b, 0x8336d030, 0x84308534}, {0xfa2a, 0xfe2f, 0x84309c38, 0x84318537}, {0xffe6, 0xffff, 0x8431a234, 0x8431a439}, {0x10000, 0x10ffff, 0x90308130, 0xe3329a35},
	}
	for _, r := range vectors {
		for _, i := range []int{0, 1} {
			ucs, gb := r[i], r[i+2]
			if got := gbMap(gb, true); got != utfWord(ucs) {
				t.Fatalf("GB%x=%x want U+%x", gb, got, ucs)
			}
			if got := gbMap(utfWord(ucs), false); got != gb {
				t.Fatalf("U+%x=%x want %x", ucs, got, gb)
			}
		}
		if gbMap(r[2]-1, true) != 0 || gbMap(r[3]+1, true) != 0 {
			t.Fatal("GB adjacent range hole", r)
		}
	}
	if utfWord(0x10000) != 0xf0908080 || utfScalar(0xf48fbfbf) != 0x10ffff || gbLinear(0x90308130) != 189000 || gbUnlinear(0) != 0x81308130 {
		t.Fatal("packed arithmetic")
	}
}
func TestBinaryEncodingDirectEdges(t *testing.T) {
	for _, tc := range []struct {
		src, dst int
		in       string
		want     uint32
		consumed int
		bad      bool
	}{
		{35, 1, "fa40", 0x8ff3f3, 2, false}, {35, 1, "f040", 0xf5a1, 2, false}, {35, 1, "eb40", 0xa2ae, 2, false}, {1, 35, "8fa1a1", 0x81ac, 3, false},
		{35, 7, "a1", 0x89a1, 1, false}, {7, 35, "89a1", 0xa1, 2, false}, {1, 7, "8ea1", 0x89a1, 2, false}, {7, 1, "89a1", 0x8ea1, 2, false},
		{35, 7, "fa40", 0x94f3f3, 2, false}, {7, 35, "94f3f3", 0xfa40, 3, false},
		{5, 41, "8fa1a1", 0xf040, 3, false}, {41, 5, "f040", 0x8fa1a1, 2, false}, {5, 41, "8fa8a1", 0xf09f, 3, false}, {41, 5, "f09f", 0x8fa8a1, 2, false},
		{5, 41, "8ffea1", 0xfc9f, 3, false}, {41, 5, "fc9f", 0x8ffea1, 2, false}, {5, 41, "8fa2a1", 0, 0, true},
		{36, 4, "c94a", 0xc4c2, 2, false}, {36, 4, "c879", 0x8ea4a1a3, 2, false}, {4, 36, "8ea4a1a3", 0xc879, 4, false},
		{36, 7, "c879", 0x9df7a1a3, 2, false}, {7, 36, "9df7a1a3", 0xc879, 4, false}, {4, 7, "8ea7a1a1", 0x9dfaa1a1, 4, false}, {7, 4, "9dfaa1a1", 0x8ea7a1a1, 4, false},
		{7, 8, "82a0", 0, 0, false}, {20, 23, "b0", 0, 0, false}, {4, 36, "8ea3a1a1", 0, 0, false},
	} {
		s := rawHex(t, tc.in)
		a, b, n, bad := Step(tc.src, tc.dst, s)
		if a != tc.want || b != 0 || n != tc.consumed || bad != tc.bad {
			t.Fatalf("%d -> %d %s: %x %x %d %v want%x %d %v", tc.src, tc.dst, tc.in, a, b, n, bad, tc.want, tc.consumed, tc.bad)
		}
	}
}
