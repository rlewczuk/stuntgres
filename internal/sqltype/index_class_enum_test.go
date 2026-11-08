package sqltype

import (
	"context"
	"testing"
)

func TestOrderingBtreeIdentityIndexClassEnumPreservation(t *testing.T) {
	enum, err := Enum(48000, "maintenance_enum", "public.maintenance_enum", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(48001, "maintenance_domain", "public.maintenance_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Domain(48002, "maintenance_nested", "public.maintenance_nested", domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	explicit, ok := LookupIndexClass("enum_ops")
	if !ok || explicit.OID() != 10069 {
		t.Fatal("released enum class missing")
	}
	for _, typ := range []*Descriptor{enum, domain, nested} {
		t.Run(typ.DiagnosticName(), func(t *testing.T) {
			implicit, ok := DefaultIndexClass(typ)
			if !ok || implicit.Name != "enum_ops" || implicit != explicit || !explicit.Accepts(typ) {
				t.Fatal("class consumer narrowed existing enum/domain ordering", implicit, ok, explicit)
			}
			ordering, err := BindOrdering(typ.Usage())
			if err != nil || ordering.DefaultBtreeOpclass() != 10069 {
				t.Fatal("original enum provider/identity changed", ordering, err)
			}
			left, err := typ.Input(context.Background(), "z", -1)
			if err != nil {
				t.Fatal(err)
			}
			right, err := typ.Input(context.Background(), "a", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, class := range []IndexClass{implicit, explicit} {
				bound, err := bindTestIndexOrdering(typ.Usage(), class)
				if err != nil {
					t.Fatal("existing declaration-order provider not consumed", err)
				}
				if got, err := bound.Compare(context.Background(), left, right, InputEnvironment{}); err != nil || got != -1 {
					t.Fatal("index class substituted lexical enum ordering", got, err)
				}
			}
		})
	}
	for _, id := range []ID{Int4, Text, VarChar, BPChar} {
		if explicit.Accepts(testTypes()[id]) {
			t.Fatal("enum preservation widened ordinary scalar admission", id)
		}
	}
}

func TestOrderingBtreeIdentityExplicitClassIdentities(t *testing.T) {
	for _, c := range []struct {
		name  string
		oid   uint32
		input ID
	}{
		{"oid_ops", 1981, Int4}, {"bpchar_ops", 10004, Text},
		{"varchar_ops", 10044, VarChar}, {"text_pattern_ops", 4217, Text},
		{"varchar_pattern_ops", 4218, VarChar}, {"bpchar_pattern_ops", 4219, BPChar},
		{"jsonb_ops", 10088, JSONB},
	} {
		t.Run(c.name, func(t *testing.T) {
			class, ok := LookupIndexClass(c.name)
			if !ok || class.OID() != c.oid || !class.Accepts(testTypes()[c.input]) {
				t.Fatal("actual released class identity/admission", class, ok, c)
			}
			if _, err := bindTestIndexOrdering(testTypes()[c.input].Usage(), class); err != nil {
				t.Fatal(err)
			}
			var oid uint32
			if n := testing.AllocsPerRun(100, func() {
				class, ok := LookupIndexClass(c.name)
				if !ok {
					panic("registered class disappeared")
				}
				oid = class.OID()
			}); n != 0 || oid != c.oid {
				t.Fatal("central class lookup/getter allocates or changes identity", n, oid)
			}
		})
	}
	array, ok := LookupIndexClass("array_ops")
	if !ok || array.OID() != 10000 || array.FamilyOID() != 397 || array.InputOID() != 2277 {
		t.Fatal("released array_ops identity", array, ok)
	}
	if _, err := bindTestIndexOrdering(testTypes()[NameArray].Usage(), array); err != nil {
		t.Fatal("released array_ops ordering", err)
	}
	record, ok := LookupIndexClass("record_ops")
	if !ok || record.OID() != 0 {
		t.Fatal("unsupported diagnostic class advertised ordering identity", record, ok)
	}
}
