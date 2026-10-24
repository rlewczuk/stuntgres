package sqltype

import "testing"

// REL_18_6 pg_cast.dat and pg_proc.dat are the independent authorities.
// Existing local row identities are contractual; newly allocated row identities
// need only be nonzero and unique. Neither automatic I/O nor array/domain
// coercions invent pg_cast rows.
func castImplementedCatalogExpected() []CastMetadata {
	rows := append([]CastMetadata(nil), castCatalogExpected()...)
	add := func(source, target ID, function uint32, context, method byte) {
		for _, row := range rows {
			if row.Source == source && row.Target == target {
				if row.Function != function || row.Context != context || row.Method != method {
					panic("conflicting independent cast fact")
				}
				return
			}
		}
		rows = append(rows, CastMetadata{Source: source, Target: target, Function: function, Context: context, Method: method})
	}
	for _, r := range []struct {
		s, t ID
		f    uint32
		c    byte
	}{
		{Name, Text, 406, 'i'}, {Name, BPChar, 408, 'a'}, {Name, VarChar, 1401, 'a'},
		{Text, Name, 407, 'i'}, {VarChar, Name, 1400, 'i'}, {BPChar, Name, 409, 'i'},
		{Char, BPChar, 860, 'a'}, {Char, VarChar, 946, 'a'}, {Text, Char, 944, 'a'}, {VarChar, Char, 944, 'a'}, {BPChar, Char, 944, 'a'}, {Char, Int4, 77, 'e'}, {Int4, Char, 78, 'e'},
		{Text, Regclass, 1079, 'i'}, {VarChar, Regclass, 1079, 'i'},
		{Int2, Bytea, 6367, 'e'}, {Int4, Bytea, 6368, 'e'}, {Int8, Bytea, 6369, 'e'}, {Bytea, Int2, 6370, 'e'}, {Bytea, Int4, 6371, 'e'}, {Bytea, Int8, 6372, 'e'},
		{Interval, Time, 1419, 'a'}, {Int4, Bit, 1683, 'e'}, {Int8, Bit, 2075, 'e'}, {Bit, Int4, 1684, 'e'}, {Bit, Int8, 2076, 'e'}, {Bit, Bit, 1685, 'i'}, {VarBit, VarBit, 1687, 'i'},
		{Macaddr, Macaddr8, 4123, 'i'}, {Macaddr8, Macaddr, 4124, 'i'}, {Inet, Cidr, 1715, 'a'},
		{JSONB, Bool, 3556, 'e'}, {JSONB, Numeric, 3449, 'e'}, {JSONB, Int2, 3450, 'e'}, {JSONB, Int4, 3451, 'e'}, {JSONB, Int8, 3452, 'e'}, {JSONB, Float4, 3453, 'e'}, {JSONB, Float8, 2580, 'e'},
		{Point, Box, 4091, 'a'}, {Lseg, Point, 1532, 'e'}, {Path, Polygon, 1449, 'a'},
		{Box, Point, 1534, 'e'}, {Box, Lseg, 1541, 'e'}, {Box, Polygon, 1448, 'a'}, {Box, Circle, 1479, 'e'},
		{Polygon, Point, 1540, 'e'}, {Polygon, Path, 1447, 'a'}, {Polygon, Box, 1446, 'e'}, {Polygon, Circle, 1474, 'e'},
		{Circle, Point, 1416, 'e'}, {Circle, Box, 1480, 'e'}, {Circle, Polygon, 1544, 'e'},
		{3904, 4451, 4281, 'e'}, {3906, 4532, 4284, 'e'}, {3926, 4536, 4296, 'e'},
	} {
		add(r.s, r.t, r.f, r.c, 'f')
	}
	for _, r := range [][2]ID{{Regproc, Regprocedure}, {Regprocedure, Regproc}, {Regoper, Regoperator}, {Regoperator, Regoper}, {Cidr, Inet}, {Bit, VarBit}, {VarBit, Bit}} {
		add(r[0], r[1], 0, 'i', 'b')
	}
	for _, alias := range []ID{Oid, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary} {
		add(Int2, alias, 313, 'i', 'f')
		add(Int4, alias, 0, 'i', 'b')
		add(Int8, alias, 1287, 'i', 'f')
		add(alias, Int4, 0, 'a', 'b')
		add(alias, Int8, 1288, 'a', 'f')
		if alias != Oid {
			add(Oid, alias, 0, 'i', 'b')
			add(alias, Oid, 0, 'i', 'b')
		}
	}
	for _, source := range []ID{Bool, Inet, Cidr} {
		function := uint32(730)
		if source == Bool {
			function = 2971
		}
		for _, target := range []ID{Text, VarChar, BPChar} {
			add(source, target, function, 'a', 'f')
		}
	}
	return rows
}

func TestCastImplementedMetadata(t *testing.T) {
	want := castImplementedCatalogExpected()
	expected := map[[2]ID]CastMetadata{}
	for _, row := range want {
		expected[[2]ID{row.Source, row.Target}] = row
	}
	if CastMetadataCount() != len(want) {
		t.Errorf("released builtin catalog row count = %d, want %d", CastMetadataCount(), len(want))
	}
	ids := map[uint32]bool{}
	seen := map[[2]ID]bool{}
	for i := 0; i < CastMetadataCount(); i++ {
		got, ok := CastMetadataAt(i)
		if !ok {
			t.Errorf("missing catalog index %d", i)
			continue
		}
		pair := [2]ID{got.Source, got.Target}
		row, exists := expected[pair]
		if !exists {
			t.Errorf("synthetic or unreleased catalog edge %+v", got)
			continue
		}
		if seen[pair] || got.OID == 0 || ids[got.OID] {
			t.Errorf("duplicate/invalid row identity %+v", got)
		}
		seen[pair] = true
		ids[got.OID] = true
		if got.Function != row.Function || got.Context != row.Context || got.Method != row.Method || (row.OID != 0 && got.OID != row.OID) {
			t.Errorf("catalog tuple %+v, want %+v", got, row)
		}
		copy := got
		copy.Function = 0
		copy.OID = 0
		again, ok := CastMetadataAt(i)
		if !ok || again != got {
			t.Errorf("metadata mutation escaped %+v -> %+v", got, again)
		}
	}
	for pair, row := range expected {
		if !seen[pair] {
			t.Errorf("missing released catalog tuple %+v", row)
		}
	}
	for _, i := range []int{-1, CastMetadataCount(), CastMetadataCount() + 1} {
		if got, ok := CastMetadataAt(i); ok || got != (CastMetadata{}) {
			t.Errorf("out-of-range index %d: %+v %t", i, got, ok)
		}
	}
	types := testTypes()
	for _, tc := range []struct {
		s, t ID
		want bool
	}{{Int4, Oid, true}, {Oid, Int4, false}, {Oid, Regclass, true}, {Regclass, Oid, true}, {Regclass, Regtype, false}, {Text, VarChar, true}, {BPChar, Text, false}, {Char, Text, false}, {PgNodeTree, Text, true}, {Cidr, Inet, true}, {Inet, Cidr, false}, {Bit, VarBit, true}, {VarBit, Bit, true}, {Int4, Int4, true}, {1007, 1016, false}} {
		if got := BinaryCoercible(types[tc.s], types[tc.t]); got != tc.want {
			t.Errorf("directional binary coercibility %d -> %d = %t, want %t", tc.s, tc.t, got, tc.want)
		}
		if tc.want {
			if _, err := PlanCoercion(types[tc.s].Usage(), types[tc.t].Usage(), Implicit); err != nil {
				t.Errorf("binary representation edge not executable %d -> %d: %v", tc.s, tc.t, err)
			}
		}
	}
	for _, tc := range []struct {
		s, t     ID
		shortcut bool
	}{
		{Oid, Int4, true}, {Int2, Oid, false}, {Char, Text, false},
		{Text, BPChar, true}, {BPChar, Text, false}, {Name, Text, false},
		{Cidr, Inet, true}, {Inet, Cidr, false}, {Bit, VarBit, true},
		{1007, 1016, false},
	} {
		p, err := PlanCoercion(types[tc.s].Usage(), types[tc.t].Usage(), Explicit)
		if err != nil || p.FunctionCastShortcut() != tc.shortcut {
			t.Errorf("selected typename operation %d -> %d: shortcut=%t want %t, err=%v", tc.s, tc.t, p.FunctionCastShortcut(), tc.shortcut, err)
		}
	}
	citext, err := Citext(49002, "cast_metadata_citext", "cast_metadata_citext")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{Text, VarChar} {
		if !BinaryCoercible(citext, types[id]) || BinaryCoercible(types[id], citext) {
			t.Error("citext implicit binary direction lost", id)
		}
	}
	domain, err := Domain(49001, "cast_metadata_domain", "cast_metadata_domain", types[Int4].Usage(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !BinaryCoercible(domain, types[Int4]) || BinaryCoercible(types[Int4], domain) {
		t.Error("domain representation may peel but must not bypass entry checks")
	}
	if BinaryCoercible(nil, types[Int4]) || BinaryCoercible(types[Int4], nil) {
		t.Error("nil descriptor is not a binary coercion")
	}
	// Generic executable I/O and array rules are real plans, not catalog rows.
	for _, pair := range [][2]ID{{Text, UUID}, {1007, 1016}} {
		if seen[pair] {
			t.Errorf("invented generic catalog row %v", pair)
		}
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err != nil {
			t.Errorf("generic plan %v: %v", pair, err)
		}
	}
}
