package sqltype

import (
	"strings"
	"testing"
)

func TestInfoSQLSizingNameBoundaries(t *testing.T) {
	d := testTypes()[Name]
	for _, tc := range []struct{ name, input, want string }{
		{"ASCII63", strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{"ASCII64", strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{"UTF8_63", strings.Repeat("é", 31) + "a", strings.Repeat("é", 31) + "a"},
		{"UTF8_64", strings.Repeat("é", 32), strings.Repeat("é", 31)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := d.Input(t.Context(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			text, err := d.Output(t.Context(), value)
			if err != nil || text != tc.want || value.Type() != d {
				t.Fatalf("NAME input %q: text=%q type=%v err=%v", tc.input, text, value.Type(), err)
			}
		})
	}
}
