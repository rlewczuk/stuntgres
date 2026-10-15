package functions

import (
	"stuntgres/internal/sqltype"
	"testing"
)

func TestResolveWindowOffset(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer, text, unknown := types[sqltype.Int4].Usage(), types[sqltype.Text].Usage(), sqltype.Usage{}
	domain, err := sqltype.Domain(20000, "offset_domain", "offset_domain", integer)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		visible, star bool
		count         int
		value, offset sqltype.Usage
		want          Resolution
	}{
		{"one", true, false, 1, text, unknown, Resolved},
		{"two", true, false, 2, integer, integer, Resolved},
		{"unknown-one", true, false, 1, unknown, unknown, UnknownPolymorphic},
		{"unknown-two", true, false, 2, unknown, integer, UnknownPolymorphic},
		{"unknown-three", true, false, 3, unknown, unknown, Resolved},
		{"offset-before-poly", true, false, 2, unknown, text, Missing},
		{"int2-offset", true, false, 2, text, types[sqltype.Int2].Usage(), Resolved},
		{"domain-offset", true, false, 3, integer, domain.Usage(), Resolved},
		{"domain-value", true, false, 1, domain.Usage(), unknown, Resolved},
		{"int8-offset", true, false, 3, integer, types[sqltype.Int8].Usage(), Missing},
		{"numeric-offset", true, false, 2, integer, types[sqltype.Numeric].Usage(), Missing},
		{"text-offset", true, false, 2, integer, text, Missing},
		{"bool-offset", true, false, 3, integer, types[sqltype.Bool].Usage(), Missing},
		{"star", true, true, 1, integer, unknown, Missing},
		{"hidden", false, false, 3, integer, integer, Missing},
		{"zero", true, false, 0, unknown, unknown, Missing},
		{"four", true, false, 4, integer, integer, Missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := ResolveWindowOffset(tc.visible, tc.star, tc.count, tc.value, tc.offset, integer); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
