package functions

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestQueryWindowNtileSignature(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer := types[sqltype.Int4].Usage()
	for _, tc := range []struct {
		name          string
		visible, star bool
		count         int
		usage         sqltype.Usage
		want          Resolution
	}{
		{"unknown", true, false, 1, sqltype.Usage{}, Resolved},
		{"int4", true, false, 1, integer, Resolved},
		{"int2", true, false, 1, types[sqltype.Int2].Usage(), Resolved},
		{"int8", true, false, 1, types[sqltype.Int8].Usage(), Missing},
		{"text", true, false, 1, types[sqltype.Text].Usage(), Missing},
		{"bool", true, false, 1, types[sqltype.Bool].Usage(), Missing},
		{"numeric", true, false, 1, types[sqltype.Numeric].Usage(), Missing},
		{"zero-arity", true, false, 0, sqltype.Usage{}, Missing},
		{"two-arity", true, false, 2, integer, Missing},
		{"invisible", false, false, 1, integer, Missing},
		{"star", true, true, 1, integer, Missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := ResolveNtile(tc.visible, tc.star, tc.count, tc.usage, integer); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8} {
		domain, err := sqltype.Domain(20000, "nt_domain", "nt_domain", types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		want := Resolved
		if id == sqltype.Int8 {
			want = Missing
		}
		if got, _ := ResolveNtile(true, false, 1, domain.Usage(), integer); got != want {
			t.Fatal("integer domain", id, got, want)
		}
	}
}
