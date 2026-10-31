package core

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestLoManageOutputOID(t *testing.T) {
	for _, tc := range []struct {
		text string
		want uint32
	}{
		{"910201", 910201}, {"  +910201trailing", 910201}, {"0910201", 910201},
		{"0x910201", 0}, {"junk", 0}, {"", 0}, {"+", 0}, {"-1", 4294967295},
		{"4294967296", 0}, {"18446744073709551615", 4294967295},
		{"18446744073709551616", 4294967295}, {"-18446744073709551616", 4294967295},
		{"-4294967295", 1}, {"\t\r\n\v\f 910202suffix", 910202},
	} {
		if got := loManageOutputOID(tc.text); got != tc.want {
			t.Errorf("atooid(%q)=%d want %d", tc.text, got, tc.want)
		}
	}
}

func TestLoManageReferenceDecision(t *testing.T) {
	var oid, text, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Oid:
			oid = d
		case sqltype.Text:
			text = d
		case sqltype.Int4:
			integer = d
		}
	}
	domain, err := sqltype.Domain(910300, "lo", "public.lo", oid.Usage())
	if err != nil {
		t.Fatal(err)
	}
	value := func(d *sqltype.Descriptor, input string) sqltype.Value {
		t.Helper()
		v, err := d.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, d := range []*sqltype.Descriptor{oid, domain} {
		old, fresh := value(d, "4294967295"), value(d, "910202")
		for _, tc := range []struct {
			name       string
			old, fresh sqltype.Value
			compare    bool
			want       uint32
			unlink     bool
		}{
			{"equal", old, old, true, 0, false},
			{"changed", old, fresh, true, 4294967295, true},
			{"to_null", old, sqltype.Null(d), true, 4294967295, true},
			{"from_null", sqltype.Null(d), fresh, true, 0, false},
			{"both_null", sqltype.Null(d), sqltype.Null(d), true, 0, false},
			{"delete", old, sqltype.Value{}, false, 4294967295, true},
		} {
			t.Run(d.Name()+"_"+tc.name, func(t *testing.T) {
				got, unlink, err := loManageReference(context.Background(), tc.old, tc.fresh, tc.compare, sqltype.OutputEnvironment{})
				if err != nil || got != tc.want || unlink != tc.unlink {
					t.Fatal(got, unlink, err)
				}
			})
		}
	}
	for _, tc := range []struct {
		d          *sqltype.Descriptor
		old, fresh string
		want       uint32
		unlink     bool
	}{
		{text, "0910201", "910201", 910201, true},
		{text, "910201suffix", "910201suffix", 0, false},
		{text, "910201suffix", "910202suffix", 910201, true},
		{integer, "910201", "910202", 910201, true},
		{integer, "910201", "910201", 0, false},
	} {
		got, unlink, err := loManageReference(context.Background(), value(tc.d, tc.old), value(tc.d, tc.fresh), true, sqltype.OutputEnvironment{})
		if err != nil || got != tc.want || unlink != tc.unlink {
			t.Fatal(tc.old, tc.fresh, got, unlink, err)
		}
	}
}

func TestLoManageUnmentionedAttribute(t *testing.T) {
	// The row slot is 1 but the stable attribute is 3 after an earlier drop.
	// A target of 2 must return before inspecting the deliberately absent rows.
	relation := &catalog.Relation{Index: map[string]int{"v": 1}, Attributes: []int32{2, 3}}
	parts := stored.TriggerParts{Name: "lm_cleanup", Level: stored.TriggerRow, Arguments: []string{"v"}}
	if err := (&StatementScope{}).manageLargeObjectReference(relation, parts, stored.TriggerUpdate, nil, nil, []int32{2}); err != nil {
		t.Fatal(err)
	}
	if err := (&StatementScope{}).manageLargeObjectReference(relation, parts, stored.TriggerInsert, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}
