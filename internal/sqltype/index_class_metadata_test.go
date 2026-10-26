package sqltype

import (
	"reflect"
	"strings"
	"testing"
)

// Independently specified released tuples; MACADDR's class OID was observed on pinned18.6.
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
		{"macaddr_ops", 829, 10024, 1984, 0, 0},
		{"pg_lsn_ops", 3220, 10067, 3253, 0, 0},
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
	want[10017] = IndexClassMetadata{10017, 783, 3550, 869, 0, "inet_ops", false}
	want[10078] = IndexClassMetadata{10078, 783, 3919, 3831, 0, "range_ops", true}
	want[10151] = IndexClassMetadata{10151, 3580, 4079, 1560, 1560, "bit_minmax_ops", true}
	want[10132] = IndexClassMetadata{10132, 3580, 4586, 1042, 1042, "bpchar_bloom_ops", false}
	want[10131] = IndexClassMetadata{10131, 3580, 4076, 1042, 1042, "bpchar_minmax_ops", true}
	want[10093] = IndexClassMetadata{10093, 3580, 4578, 17, 17, "bytea_bloom_ops", false}
	want[10092] = IndexClassMetadata{10092, 3580, 4064, 17, 17, "bytea_minmax_ops", true}
	want[10095] = IndexClassMetadata{10095, 3580, 4577, 18, 18, "char_bloom_ops", false}
	want[10094] = IndexClassMetadata{10094, 3580, 4062, 18, 18, "char_minmax_ops", true}
	want[10138] = IndexClassMetadata{10138, 3580, 4576, 1082, 1082, "date_bloom_ops", false}
	want[10137] = IndexClassMetadata{10137, 3580, 4605, 1082, 1082, "date_minmax_multi_ops", false}
	want[10136] = IndexClassMetadata{10136, 3580, 4059, 1082, 1082, "date_minmax_ops", true}
	want[10117] = IndexClassMetadata{10117, 3580, 4582, 700, 700, "float4_bloom_ops", false}
	want[10116] = IndexClassMetadata{10116, 3580, 4608, 700, 700, "float4_minmax_multi_ops", false}
	want[10115] = IndexClassMetadata{10115, 3580, 4070, 700, 700, "float4_minmax_ops", true}
	want[10120] = IndexClassMetadata{10120, 3580, 4582, 701, 701, "float8_bloom_ops", false}
	want[10119] = IndexClassMetadata{10119, 3580, 4608, 701, 701, "float8_minmax_multi_ops", false}
	want[10118] = IndexClassMetadata{10118, 3580, 4070, 701, 701, "float8_minmax_ops", true}
	want[10129] = IndexClassMetadata{10129, 3580, 4585, 869, 869, "inet_bloom_ops", false}
	want[10130] = IndexClassMetadata{10130, 3580, 4102, 869, 869, "inet_inclusion_ops", true}
	want[10128] = IndexClassMetadata{10128, 3580, 4611, 869, 869, "inet_minmax_multi_ops", false}
	want[10127] = IndexClassMetadata{10127, 3580, 4075, 869, 869, "inet_minmax_ops", false}
	want[10103] = IndexClassMetadata{10103, 3580, 4572, 21, 21, "int2_bloom_ops", false}
	want[10102] = IndexClassMetadata{10102, 3580, 4602, 21, 21, "int2_minmax_multi_ops", false}
	want[10101] = IndexClassMetadata{10101, 3580, 4054, 21, 21, "int2_minmax_ops", true}
	want[10106] = IndexClassMetadata{10106, 3580, 4572, 23, 23, "int4_bloom_ops", false}
	want[10105] = IndexClassMetadata{10105, 3580, 4602, 23, 23, "int4_minmax_multi_ops", false}
	want[10104] = IndexClassMetadata{10104, 3580, 4054, 23, 23, "int4_minmax_ops", true}
	want[10100] = IndexClassMetadata{10100, 3580, 4572, 20, 20, "int8_bloom_ops", false}
	want[10099] = IndexClassMetadata{10099, 3580, 4602, 20, 20, "int8_minmax_multi_ops", false}
	want[10098] = IndexClassMetadata{10098, 3580, 4054, 20, 20, "int8_minmax_ops", true}
	want[10147] = IndexClassMetadata{10147, 3580, 4588, 1186, 1186, "interval_bloom_ops", false}
	want[10146] = IndexClassMetadata{10146, 3580, 4613, 1186, 1186, "interval_minmax_multi_ops", false}
	want[10145] = IndexClassMetadata{10145, 3580, 4078, 1186, 1186, "interval_minmax_ops", true}
	want[10126] = IndexClassMetadata{10126, 3580, 4584, 774, 774, "macaddr8_bloom_ops", false}
	want[10125] = IndexClassMetadata{10125, 3580, 4610, 774, 774, "macaddr8_minmax_multi_ops", false}
	want[10124] = IndexClassMetadata{10124, 3580, 4109, 774, 774, "macaddr8_minmax_ops", true}
	want[10123] = IndexClassMetadata{10123, 3580, 4583, 829, 829, "macaddr_bloom_ops", false}
	want[10122] = IndexClassMetadata{10122, 3580, 4609, 829, 829, "macaddr_minmax_multi_ops", false}
	want[10121] = IndexClassMetadata{10121, 3580, 4074, 829, 829, "macaddr_minmax_ops", true}
	want[10097] = IndexClassMetadata{10097, 3580, 4579, 19, 19, "name_bloom_ops", false}
	want[10096] = IndexClassMetadata{10096, 3580, 4065, 19, 19, "name_minmax_ops", true}
	want[10155] = IndexClassMetadata{10155, 3580, 4574, 1700, 1700, "numeric_bloom_ops", false}
	want[10154] = IndexClassMetadata{10154, 3580, 4603, 1700, 1700, "numeric_minmax_multi_ops", false}
	want[10153] = IndexClassMetadata{10153, 3580, 4055, 1700, 1700, "numeric_minmax_ops", true}
	want[10111] = IndexClassMetadata{10111, 3580, 4580, 26, 26, "oid_bloom_ops", false}
	want[10110] = IndexClassMetadata{10110, 3580, 4606, 26, 26, "oid_minmax_multi_ops", false}
	want[10109] = IndexClassMetadata{10109, 3580, 4068, 26, 26, "oid_minmax_ops", true}
	want[10108] = IndexClassMetadata{10108, 3580, 4573, 25, 25, "text_bloom_ops", false}
	want[10107] = IndexClassMetadata{10107, 3580, 4056, 25, 25, "text_minmax_ops", true}
	want[10113] = IndexClassMetadata{10113, 3580, 4581, 27, 27, "tid_bloom_ops", false}
	want[10114] = IndexClassMetadata{10114, 3580, 4607, 27, 27, "tid_minmax_multi_ops", false}
	want[10112] = IndexClassMetadata{10112, 3580, 4069, 27, 27, "tid_minmax_ops", true}
	want[10135] = IndexClassMetadata{10135, 3580, 4587, 1083, 1083, "time_bloom_ops", false}
	want[10134] = IndexClassMetadata{10134, 3580, 4612, 1083, 1083, "time_minmax_multi_ops", false}
	want[10133] = IndexClassMetadata{10133, 3580, 4077, 1083, 1083, "time_minmax_ops", true}
	want[10141] = IndexClassMetadata{10141, 3580, 4576, 1114, 1114, "timestamp_bloom_ops", false}
	want[10140] = IndexClassMetadata{10140, 3580, 4605, 1114, 1114, "timestamp_minmax_multi_ops", false}
	want[10139] = IndexClassMetadata{10139, 3580, 4059, 1114, 1114, "timestamp_minmax_ops", true}
	want[10144] = IndexClassMetadata{10144, 3580, 4576, 1184, 1184, "timestamptz_bloom_ops", false}
	want[10143] = IndexClassMetadata{10143, 3580, 4605, 1184, 1184, "timestamptz_minmax_multi_ops", false}
	want[10142] = IndexClassMetadata{10142, 3580, 4059, 1184, 1184, "timestamptz_minmax_ops", true}
	want[10150] = IndexClassMetadata{10150, 3580, 4575, 1266, 1266, "timetz_bloom_ops", false}
	want[10149] = IndexClassMetadata{10149, 3580, 4604, 1266, 1266, "timetz_minmax_multi_ops", false}
	want[10148] = IndexClassMetadata{10148, 3580, 4058, 1266, 1266, "timetz_minmax_ops", true}
	want[10158] = IndexClassMetadata{10158, 3580, 4589, 2950, 2950, "uuid_bloom_ops", false}
	want[10157] = IndexClassMetadata{10157, 3580, 4614, 2950, 2950, "uuid_minmax_multi_ops", false}
	want[10156] = IndexClassMetadata{10156, 3580, 4081, 2950, 2950, "uuid_minmax_ops", true}
	want[10152] = IndexClassMetadata{10152, 3580, 4080, 1562, 1562, "varbit_minmax_ops", true}
	for _, m := range []IndexClassMetadata{
		{10060, 783, 2593, 603, 0, "box_ops", true},
		{10061, 783, 1029, 600, 603, "point_ops", true},
		{10062, 783, 2594, 604, 603, "poly_ops", true},
		{10063, 783, 2595, 718, 603, "circle_ops", true},
		{10072, 783, 3655, 3614, 3642, "tsvector_ops", true},
		{10075, 783, 3702, 3615, 20, "tsquery_ops", true},
		{10082, 783, 6158, 4537, 3831, "multirange_ops", true},
		{10018, 4000, 3794, 869, 0, "inet_ops", true},
		{10079, 4000, 3474, 3831, 0, "range_ops", true},
		{10083, 4000, 5000, 603, 0, "box_ops", true},
		{10084, 4000, 4015, 600, 0, "quad_point_ops", true},
		{10085, 4000, 4016, 600, 0, "kd_point_ops", false},
		{10086, 4000, 4017, 25, 0, "text_ops", true},
		{10087, 4000, 5008, 604, 603, "poly_ops", true},
	} {
		want[m.OID] = m
	}
	return want
}

func TestIndexClassMetadataInventory(t *testing.T) {
	want := indexClassExpected()
	if IndexClassMetadataCount() != 145 || len(want) != 145 {
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
	for _, i := range []int{-1, IndexClassMetadataCount(), IndexClassMetadataCount() + 1} {
		if m, ok := IndexClassMetadataAt(i); ok || m != (IndexClassMetadata{}) {
			t.Fatal("bounds", i, m, ok)
		}
	}
	m, _ := IndexClassMetadataAt(0)
	m.Name = "changed"
	if again, _ := IndexClassMetadataAt(0); again.Name == m.Name {
		t.Fatal("metadata is mutable")
	}
	for _, name := range []string{"array_ops", "record_ops", "record_image_ops", "bit_ops", "varbit_ops", "network_ops"} {
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
		if m.Method == 3580 {
			c, ok := LookupBrinIndexClass(m.Name)
			if !ok || !c.IsBrin() || c.OID() != m.OID || c.FamilyOID() != m.Family || c.InputOID() != m.Input || c.StorageOID() != m.KeyType {
				t.Fatal("BRIN catalog identity", m, c)
			}
			if BrinMinmaxIdentity(c) != strings.HasSuffix(m.Name, "_minmax_ops") {
				t.Fatal("BRIN algorithm capability", m, c)
			}
			continue
		}
		if m.Method == 4000 {
			c, ok := LookupSPGiSTIndexClass(m.Name)
			if !ok || !c.IsSPGiST() || c.IsGiST() || c.OID() != m.OID || c.FamilyOID() != m.Family || c.StorageOID() != m.KeyType {
				t.Fatal("SP-GiST identity", m, c)
			}
			continue
		}
		if m.Method == 783 {
			c, ok := LookupGiSTIndexClass(m.Name)
			if !ok || !c.IsGiST() || c.IsHash() || c.OID() != m.OID {
				t.Fatal("GiST identity", m, c)
			}
			continue // Concrete predicate behavior is covered by TestExclusionGiSTOperators.
		}
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
				if _, err := bindTestIndexOrdering(typ.Usage(), c); err != nil {
					t.Fatal("advertised ordering", m, err)
				}
			} else {
				if _, err := bindTestIndexHash(typ.Usage(), c); err != nil {
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
				if _, err := bindTestIndexHash(types[id].Usage(), c); err != nil {
					t.Fatal(id, err)
				}
			} else {
				if _, err := bindTestIndexOrdering(types[id].Usage(), c); err != nil {
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
