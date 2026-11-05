package fixture

import (
	"encoding/binary"
	"testing"
)

func TestExternalSQLProviderAuthObservation(t *testing.T) {
	frame := func(kind byte, payload []byte) []byte {
		b := make([]byte, 5+len(payload))
		b[0] = kind
		binary.BigEndian.PutUint32(b[1:], uint32(4+len(payload)))
		copy(b[5:], payload)
		return b
	}
	auth := func(codes ...uint32) []byte {
		var b []byte
		for _, code := range codes {
			p := make([]byte, 4)
			binary.BigEndian.PutUint32(p, code)
			// Bytes beyond the code are deliberately irrelevant to observation.
			p = append(p, []byte("not-retained")...)
			b = append(b, frame('R', p)...)
		}
		return b
	}
	for _, tc := range []struct {
		name  string
		input []byte
		want  string
	}{
		{"trust", auth(0), "none"},
		{"password", auth(3, 0), "password"},
		{"md5", auth(5, 0), "md5"},
		{"scram", auth(10, 11, 12, 0), "scram-sha-256"},
		{"no-success", auth(3), "unknown"},
		{"incomplete-sasl", auth(10, 11, 0), "unknown"},
		{"unknown", auth(7, 0), "unknown"},
		{"error", append(auth(3), frame('E', []byte("ignored"))...), "unknown"},
		{"interleaved", append(frame('N', []byte("ignored")), auth(5, 0)...), "md5"},
		{"short-length", []byte{'R', 0, 0, 0, 3}, "unknown"},
		{"short-auth", frame('R', []byte{0}), "unknown"},
		{"oversized", []byte{'R', 127, 255, 255, 255}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for width := 1; width <= len(tc.input); width++ {
				var o externalSQLAuthObservation
				for offset := 0; offset < len(tc.input); offset += width {
					end := offset + width
					if end > len(tc.input) {
						end = len(tc.input)
					}
					o.consume(tc.input[offset:end])
				}
				if got := o.mechanism(); got != tc.want {
					t.Fatalf("fragment width %d: got %q want %q", width, got, tc.want)
				}
				if o.done {
					o.consume(auth(3, 0))
					if got := o.mechanism(); got != tc.want {
						t.Fatal("post-auth traffic changed observation", got)
					}
				}
			}
		})
	}
}
