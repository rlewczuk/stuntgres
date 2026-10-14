package functions

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestQueryWindowValueSignature(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer := types[sqltype.Int4].Usage()
	text := types[sqltype.Text].Usage()
	for _, tc := range []struct {
		name               string
		visible, star, nth bool
		count              int
		value, ordinal     sqltype.Usage
		want               Resolution
	}{
		{"first", true, false, false, 1, text, sqltype.Usage{}, Resolved},
		{"nth-unknown-ordinal", true, false, true, 2, text, sqltype.Usage{}, Resolved},
		{"nth-int4", true, false, true, 2, text, integer, Resolved},
		{"nth-int2", true, false, true, 2, text, types[sqltype.Int2].Usage(), Resolved},
		{"nth-int8", true, false, true, 2, text, types[sqltype.Int8].Usage(), Missing},
		{"unknown-value", true, false, false, 1, sqltype.Usage{}, sqltype.Usage{}, UnknownPolymorphic},
		{"unknown-value-invalid-ordinal", true, false, true, 2, sqltype.Usage{}, text, Missing},
		{"star", true, true, false, 1, text, sqltype.Usage{}, Missing},
		{"invisible", false, false, false, 1, text, sqltype.Usage{}, Missing},
		{"wrong-arity", true, false, true, 1, text, sqltype.Usage{}, Missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := ResolveWindowValue(tc.visible, tc.star, tc.nth, tc.count, tc.value, tc.ordinal, integer); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	domain, err := sqltype.Domain(20000, "window_domain", "window_domain", text)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ResolveWindowValue(true, false, false, 1, domain.Usage(), sqltype.Usage{}, integer); got != Resolved {
		t.Fatal("concrete domain rejected")
	}
}
