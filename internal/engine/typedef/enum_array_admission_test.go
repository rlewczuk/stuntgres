package typedef

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestEnumArrayAdmission(t *testing.T) {
	mood, err := sqltype.Enum(70001, "enum_array_mood", "public.enum_array_mood", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(70002, "_enum_array_mood", mood)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := sqltype.Domain(70003, "enum_array_wrapped", "public.enum_array_wrapped", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrappedArray, err := sqltype.Array(70004, "_enum_array_wrapped", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{mood, array, wrapped, wrappedArray} {
		if !StoredArrayElement(typ) {
			t.Fatal("enum element refused for stored array", typ.Name())
		}
	}
	for _, id := range []sqltype.ID{sqltype.Macaddr, sqltype.Inet, sqltype.Cidr, sqltype.Xid, sqltype.PgSnapshot, sqltype.Lseg, sqltype.Circle} {
		found := false
		for _, d := range sqltype.Builtins() {
			if d.ID() != id {
				continue
			}
			found = true
			if StoredArrayElement(d) {
				t.Fatal("refused element kind admitted for stored array", d.Name())
			}
		}
		if !found {
			t.Fatal("builtin element kind has no descriptor", id)
		}
	}
	// Money is an admitted scalar element; its array is lookup-only and the
	// refusal belongs to the builtin array descriptor, not element admission.
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Money && !StoredArrayElement(d) {
			t.Fatal("money scalar element admission changed")
		}
	}
	// The lookup-only builtin array classifier remains unchanged: enums are
	// admitted here because their OIDs are user-defined, not by widening it.
	if sqltype.ArrayElementAdmitted(mood.ID()) {
		t.Fatal("builtin array admission surface widened")
	}
}
