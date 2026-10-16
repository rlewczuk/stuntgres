package sqltype

import (
	"reflect"
	"strings"
	"testing"
)

// Independently specified tuples confirmed by the pinned18.0 public oracle.
func indexClassExpected() map[uint32]IndexClassMetadata {
	want := map[uint32]IndexClassMetadata{}
	for _, r := range []struct {
		name                string
		input, b, bf, h, hf uint32
	}{
		{"bool_ops", 16, 10003, 424, 10048, 2222}, {"bytea_ops", 17, 10006, 428, 10049, 2223},
		{"char_ops", 18, 10007, 429, 10008, 431}, {"name_ops", 19, 10028, 1994, 10029, 1995},
		{"int2_ops", 21, 1979, 1976, 10019, 1977}, {"int4_ops", 23, 1978, 1976, 10020, 1977},
		{"int8_ops", 20, 3124, 1976, 10021, 1977}, {"text_ops", 25, 3126, 1994, 10037, 1995},
		{"varchar_ops", 25, 10044, 1994, 10045, 1995}, {"bpchar_ops", 1042, 10004, 426, 10005, 427},
		{"oid_ops", 26, 1981, 1989, 10031, 1990}, {"xid8_ops", 5069, 10053, 5067, 10052, 5032},
		{"tid_ops", 27, 10050, 2789, 10055, 2227},
		{"xid_ops", 28, 0, 0, 10051, 2225}, {"cid_ops", 29, 0, 0, 10054, 2226},
		{"float4_ops", 700, 10012, 1970, 10013, 1971}, {"float8_ops", 701, 3123, 1970, 10014, 1971},
		{"date_ops", 1082, 3122, 434, 10011, 435}, {"time_ops", 1083, 10038, 1996, 10039, 1997},
		{"timestamp_ops", 1114, 3128, 434, 10046, 2040}, {"timestamptz_ops", 1184, 3127, 434, 10040, 1999},
		{"timetz_ops", 1266, 10041, 2000, 10042, 2001}, {"interval_ops", 1186, 10022, 1982, 10023, 1983},
		{"numeric_ops", 1700, 3125, 1988, 10030, 1998}, {"money_ops", 790, 10047, 2099, 0, 0},
		{"uuid_ops", 2950, 10065, 2968, 10066, 2969}, {"inet_ops", 869, 10015, 1974, 0, 0},
		{"jsonb_ops", 3802, 10088, 4033, 10089, 4034}, {"text_pattern_ops", 25, 4217, 2095, 10056, 2229},
		{"varchar_pattern_ops", 25, 4218, 2095, 10057, 2229}, {"bpchar_pattern_ops", 1042, 4219, 2097, 10058, 2231},
		{"enum_ops", 3500, 10069, 3522, 10070, 3523},
	} {
		def := r.name != "varchar_ops" && !strings.Contains(r.name, "pattern")
		if r.b != 0 {
			key := uint32(0)
			if r.name == "name_ops" {
				key = 2275
			}
			want[r.b] = IndexClassMetadata{r.b, 403, r.bf, r.input, key, r.name, def}
		}
		if r.h != 0 {
			want[r.h] = IndexClassMetadata{r.h, 405, r.hf, r.input, 0, r.name, def}
		}
	}
	return want
}

func TestIndexClassMetadataInventory(t *testing.T) {
	want := indexClassExpected()
	if IndexClassMetadataCount() != 60 || len(want) != 60 {
		t.Fatal("count", IndexClassMetadataCount(), len(want))
	}
	got := map[uint32]IndexClassMetadata{}
	keys := map[struct {
		method uint32
		name   string
	}]bool{}
	defaults := map[[2]uint32]bool{}
	for i := range IndexClassMetadataCount() {
		m, ok := IndexClassMetadataAt(i)
		key := struct {
			method uint32
			name   string
		}{m.Method, m.Name}
		if !ok || m.OID == 0 || got[m.OID].OID != 0 || keys[key] {
			t.Fatal("duplicate/invalid", i, m, ok)
		}
		got[m.OID], keys[key] = m, true
		if m.Default {
			k := [2]uint32{m.Method, m.Input}
			if defaults[k] {
				t.Fatal("duplicate default", m)
			}
			defaults[k] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata got %#v want %#v", got, want)
	}
	for _, i := range []int{-1, 60, 61} {
		if m, ok := IndexClassMetadataAt(i); ok || m != (IndexClassMetadata{}) {
			t.Fatal("bounds", i, m, ok)
		}
	}
	m, _ := IndexClassMetadataAt(0)
	m.Name = "changed"
	if again, _ := IndexClassMetadataAt(0); again.Name == m.Name {
		t.Fatal("metadata is mutable")
	}
	for _, name := range []string{"array_ops", "record_ops", "record_image_ops", "range_ops", "multirange_ops", "bit_ops", "varbit_ops", "pg_lsn_ops", "tsquery_ops", "tsvector_ops", "network_ops"} {
		for _, m := range got {
			if m.Name == name {
				t.Fatal("diagnostic-only class advertised", m)
			}
		}
	}
}

func TestIndexClassMetadataProviders(t *testing.T) {
	types := testTypes()
	enum, err := Enum(48000, "class_enum", "public.class_enum", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range IndexClassMetadataCount() {
		m, _ := IndexClassMetadataAt(i)
		typ := types[ID(m.Input)]
		if m.Input == 3500 {
			typ = enum
		}
		if typ == nil || !typ.Concrete() {
			t.Fatal("no concrete owner", m)
		}
		var c IndexClass
		var ok bool
		if m.Method == 403 {
			c, ok = LookupIndexClass(m.Name)
		} else {
			c, ok = LookupHashIndexClass(m.Name)
		}
		if !ok || c.Name != m.Name || c.OID() != m.OID || c.IsHash() != (m.Method == 405) || !c.Accepts(typ) {
			t.Fatal("lookup disagreement", m, c, ok)
		}
		bind := func(typ *Descriptor) {
			t.Helper()
			if m.Method == 403 {
				if _, err := BindIndexOrdering(typ.Usage(), c); err != nil {
					t.Fatal("advertised ordering", m, err)
				}
			} else {
				if _, err := BindIndexHash(typ.Usage(), c); err != nil {
					t.Fatal("advertised hash", m, err)
				}
			}
		}
		bind(typ)
		if typ.ID() != Char {
			domain, err := Domain(49000+ID(i), "class_domain", "public.class_domain", typ.Usage())
			if err != nil {
				t.Fatal(err)
			}
			bind(domain)
		}
		if m.Default {
			var def IndexClass
			if m.Method == 403 {
				def, ok = DefaultIndexClass(typ)
			} else {
				def, ok = DefaultHashIndexClass(typ)
			}
			if !ok || def.OID() != m.OID {
				t.Fatal("default disagreement", m, def)
			}
		}
	}
	for _, id := range []ID{VarChar, Cidr, Regclass, Regtype, Regrole} {
		for _, hash := range []bool{false, true} {
			if id == Cidr && hash {
				continue
			} // No owned hash-network provider.
			c, ok := DefaultIndexClass(types[id])
			if hash {
				c, ok = DefaultHashIndexClass(types[id])
			}
			if !ok {
				t.Fatal("alias default", id, hash)
			}
			if hash {
				if _, err := BindIndexHash(types[id].Usage(), c); err != nil {
					t.Fatal(id, err)
				}
			} else {
				if _, err := BindIndexOrdering(types[id].Usage(), c); err != nil {
					t.Fatal(id, err)
				}
			}
		}
	}
}

func TestIndexFamilyMetadataAuthority(t *testing.T) {
	seen := map[uint32]IndexFamilyMetadata{}
	keys := map[struct {
		method uint32
		name   string
	}]uint32{}
	for i := range IndexClassMetadataCount() {
		class, _ := IndexClassMetadataAt(i)
		family, ok := IndexFamilyMetadataByOID(class.Family)
		if !ok || family.OID != class.Family || family.Method != class.Method || family.Name == "" {
			t.Fatal("unresolved owned family", class, family, ok)
		}
		key := struct {
			method uint32
			name   string
		}{family.Method, family.Name}
		if old := keys[key]; old != 0 && old != family.OID {
			t.Fatal("duplicate family symbolic identity", old, family)
		}
		keys[key] = family.OID
		seen[family.OID] = family
	}
	for _, want := range []IndexFamilyMetadata{{2789, 403, "tid_ops"}, {2227, 405, "tid_ops"}} {
		if got := seen[want.OID]; got != want {
			t.Fatal("released TID family", got, want)
		}
	}
	for _, want := range []IndexFamilyMetadata{{1976, 403, "integer_ops"}, {1977, 405, "integer_ops"}, {434, 403, "datetime_ops"}, {1974, 403, "network_ops"}, {1994, 403, "text_ops"}, {1995, 405, "text_ops"}, {2229, 405, "text_pattern_ops"}} {
		if got := seen[want.OID]; got != want {
			t.Fatal("released family", got, want)
		}
	}
	for _, id := range []uint32{0, 397, 627, 999999} {
		if got, ok := IndexFamilyMetadataByOID(id); ok || got != (IndexFamilyMetadata{}) {
			t.Fatal("unowned family advertised", id, got, ok)
		}
	}
}
