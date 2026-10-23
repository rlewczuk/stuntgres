package sqltype

import "testing"

func TestCastDefinitionIODescriptorVolatility(t *testing.T) {
	types := testTypes()
	enum, err := Enum(60001, "cast_enum", "public.cast_enum", []string{"red"})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := Composite(60002, "cast_record", "public.cast_record", 0, []CompositeMember{{Attribute: 1, Name: "v", Usage: types[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	// REL_18_6 pg_proc.dat declares both directions STABLE even when every
	// member/subtype codec is immutable. Domain codecs delegate to their base.
	for _, typ := range []*Descriptor{enum, composite, types[Int4Range], types[Int8Range], types[NumRange], types[DateRange], types[TimestampRange], types[TimestamptzRange], types[Int4Multirange], types[Int8Multirange], types[NumMultirange], types[1007]} {
		domain, err := Domain(60003, "cast_domain", "public.cast_domain", typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []*Descriptor{typ, domain} {
			if !d.InputSessionDependent() || !d.OutputSessionDependent() {
				t.Fatalf("%s lost released STABLE input/output classification", d.Name())
			}
			for _, input := range []bool{false, true} {
				from, to := d.Usage(), types[Text].Usage()
				if input {
					from, to = to, from
				}
				p := CoercionPlan{source: from, target: to, context: Explicit, method: viaIO}
				if !p.SessionDependent() {
					t.Fatalf("%s input=%v lost IO volatility", d.Name(), input)
				}
			}
		}
	}
	for _, typ := range []*Descriptor{types[Xid], types[Xid8], types[Int4]} {
		domain, err := Domain(60004, "cast_immutable", "public.cast_immutable", typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if domain.InputSessionDependent() || domain.OutputSessionDependent() {
			t.Fatal("domain delegation acquired domain_in volatility", typ.Name())
		}
	}
	if types[Xid].OutputSessionDependent() || types[Xid8].OutputSessionDependent() || types[Float4].InputSessionDependent() {
		t.Fatal("immutable XID/XID8 output and REAL input changed")
	}
}
