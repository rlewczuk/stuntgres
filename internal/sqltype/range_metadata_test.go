package sqltype

import "testing"

func TestRangeMetadata(t *testing.T) {
	types := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	facts := []struct {
		metadata  RangeMetadata
		class     string
		companion bool
	}{
		{RangeMetadata{3904, 23, 4451, 0, 1978, 3914, 3922}, "int4_ops", true},
		{RangeMetadata{3906, 1700, 4532, 0, 3125, 0, 3924}, "numeric_ops", true},
		{RangeMetadata{3908, 1114, 4533, 0, 3128, 0, 3929}, "timestamp_ops", false},
		{RangeMetadata{3910, 1184, 4534, 0, 3127, 0, 3930}, "timestamptz_ops", false},
		{RangeMetadata{3912, 1082, 4535, 0, 3122, 3915, 3925}, "date_ops", false},
		{RangeMetadata{3926, 20, 4536, 0, 3124, 3928, 3923}, "int8_ops", true},
	}
	for _, f := range facts {
		d := types[ID(f.metadata.Type)]
		got, ok := d.RangeMetadata()
		if !ok || got != f.metadata || !d.Concrete() || d.rangeSubtype != types[ID(f.metadata.Subtype)] {
			t.Fatal("range metadata/subtype", f, got, ok)
		}
		class, ok := LookupIndexClass(f.class)
		if !ok || class.OID() != f.metadata.Subclass || uint32(class.input) != f.metadata.Subtype {
			t.Fatal("subtype btree class", f, class)
		}
		if f.companion {
			if d.multirangeCompanion != types[ID(f.metadata.Multitype)] || !d.multirangeCompanion.Concrete() || d.multirangeCompanion.multirangeRange != d {
				t.Fatal("executable companion", f)
			}
		} else if d.multirangeCompanion != nil || types[ID(f.metadata.Multitype)].Concrete() {
			t.Fatal("lookup-only temporal companion promoted", f)
		}
		got.Subtype = 0
		again, _ := d.RangeMetadata()
		if again != f.metadata {
			t.Fatal("metadata was not a value copy")
		}
		domain, err := Domain(90000+ID(f.metadata.Type), "range_domain", "range_domain", d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := domain.RangeMetadata(); ok {
			t.Fatal("domain acquired own range row")
		}
		for _, bad := range []*Descriptor{
			{id: d.id, lookupOnly: true, rangeSubtype: d.rangeSubtype},
			{id: d.id, rangeSubtype: types[Bool]},
			{id: d.id, rangeSubtype: d.rangeSubtype, multirangeCompanion: types[Int4]},
		} {
			if _, ok := bad.RangeMetadata(); ok {
				t.Fatal("contradictory/lookup range admitted", bad)
			}
		}
	}
	var nilDescriptor *Descriptor
	if _, ok := nilDescriptor.RangeMetadata(); ok {
		t.Fatal("nil admitted")
	}
	for _, d := range types {
		if isRange(d.id) {
			continue
		}
		if _, ok := d.RangeMetadata(); ok {
			t.Fatal("nonrange admitted", d.ID())
		}
	}
}
