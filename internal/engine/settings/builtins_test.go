package settings

import (
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestApplicationNameNormalizer(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		clipped     bool
	}{
		{strings.Repeat("a", 64), strings.Repeat("a", 63), true},
		{strings.Repeat("a", 62) + "é", strings.Repeat("a", 62), true},
		{"é", `\xc3\xa9`, false},
	} {
		got, n, err := normalizeApplicationName(tc.input)
		if err != nil || got != tc.want || (n != nil) != tc.clipped {
			t.Fatalf("%q: %q %#v %v", tc.input, got, n, err)
		}
		if n != nil {
			want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.input + `" will be truncated to "` + tc.want + `"`}
			if *n != want {
				t.Fatalf("%#v", n)
			}
		}
	}
}
