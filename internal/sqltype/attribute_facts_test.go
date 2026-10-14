package sqltype

import "testing"

func TestAttributeTypeFacts(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id             ID
		length         int16
		byval          bool
		align, storage string
	}{
		{Bool, 1, true, "c", "p"}, {Int2, 2, true, "s", "p"}, {Int4, 4, true, "i", "p"}, {Int8, 8, true, "d", "p"}, {Name, 64, false, "c", "p"}, {Text, -1, false, "i", "x"}, {Numeric, -1, false, "i", "m"}, {UUID, 16, false, "c", "p"}, {Interval, 16, false, "d", "p"}, {Timetz, 12, false, "d", "p"}, {ACLItem, 16, false, "d", "p"}, {Inet, -1, false, "i", "m"}, {Cidr, -1, false, "i", "m"}, {1007, -1, false, "i", "x"}, {1022, -1, false, "d", "x"}, {1003, -1, false, "i", "x"}, {Int4Range, -1, false, "i", "x"}, {Int8Range, -1, false, "d", "x"}, {Int4Multirange, -1, false, "i", "x"}, {Int8Multirange, -1, false, "d", "x"},
	} {
		d := types[tc.id]
		if d == nil {
			t.Fatal("missing type", tc.id)
		}
		want := PostgreSQLAttributeFacts{Length: tc.length, ByValue: tc.byval, Alignment: tc.align, Storage: tc.storage}
		if got := d.AttributeFacts(); got != want {
			t.Fatal(tc.id, got, want)
		}
	}
	domain, err := Domain(200000, "fact_domain", "fact_domain", types[Numeric].Usage())
	if err != nil {
		t.Fatal(err)
	}
	if domain.AttributeFacts() != (PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "m"}) {
		t.Fatal("domain physical facts", domain.AttributeFacts())
	}
}

func TestAttributeCatalogNullCarrier(t *testing.T) {
	var canonical *Descriptor
	for _, d := range Builtins() {
		if d.ID() == 2277 {
			canonical = d
		}
	}
	if canonical == nil || canonical.Concrete() || canonical.Usage().Valid() || !canonical.Usage().CatalogNullUsage() {
		t.Fatal("bounded pseudo identity", canonical)
	}
	u := canonical.Usage()
	v := CatalogResultNull(u)
	if !v.IsNull() || !v.ValidResult(u) || v.Type() != canonical {
		t.Fatal("NULL result", v)
	}
	if (Value{typ: canonical}).ValidResult(u) {
		t.Fatal("nonnull ANYARRAY escaped")
	}
	copy := *canonical
	if copy.Usage().CatalogNullUsage() {
		t.Fatal("copied descriptor manufactured canonical authority")
	}
	for _, bad := range []Usage{{Type: LookupOnly(2277, "anyarray", 0), Typmod: -1}, {Type: canonical, Typmod: 0}, {Type: canonical, Typmod: -1, Collation: CollationRef{ID: 950}}} {
		if bad.CatalogNullUsage() || v.ValidResult(bad) {
			t.Fatal("forged/modified usage escaped", bad)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("ordinary NULL admitted ANYARRAY")
			}
		}()
		_ = Null(canonical)
	}()
	if _, err := canonical.Input(t.Context(), "{}", -1); err == nil {
		t.Fatal("pseudo input became executable")
	}
	if _, err := BindOrdering(u); err == nil {
		t.Fatal("pseudo ordering became executable")
	}
}
