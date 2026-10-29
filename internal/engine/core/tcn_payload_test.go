package core

import (
	"strings"
	"testing"
)

func TestTcnPayload(t *testing.T) {
	for _, c := range []struct {
		text  string
		quote byte
		want  string
	}{
		{"", '\'', "''"},
		{"O'Brien,west", '\'', "'O''Brien,west'"},
		{"é\\,=", '\'', "'é\\,='"},
		{`t"cn`, '"', `"t""cn"`},
		{`k"b`, '"', `"k""b"`},
	} {
		var b strings.Builder
		tcnQuote(&b, c.text, c.quote)
		if b.String() != c.want {
			t.Fatalf("quote(%q) = %q, want %q", c.text, b.String(), c.want)
		}
	}
}
