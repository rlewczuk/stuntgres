package copyfmt

import (
	"strings"
	"testing"
)

func TestCopyCSVContext(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"ascii99", strings.Repeat("x", 99), strings.Repeat("x", 99)},
		{"ascii100", strings.Repeat("x", 100), strings.Repeat("x", 100)},
		{"ascii101", strings.Repeat("x", 101), strings.Repeat("x", 100) + "..."},
		{"unicode100", strings.Repeat("é", 50), strings.Repeat("é", 50)},
		{"unicode102", strings.Repeat("é", 51), strings.Repeat("é", 50) + "..."},
		{"raw-opening-quote", "\"" + strings.Repeat("é", 60) + "\",x", "\"" + strings.Repeat("é", 49) + "..."},
		{"three-byte-boundary", strings.Repeat("x", 99) + "漢", strings.Repeat("x", 99) + "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContextValue(tc.input); got != tc.want {
				t.Fatalf("Context %q want %q", got, tc.want)
			}
		})
	}
}
