package sqltype

import "testing"

func TestPGTypeBuiltinFacts(t *testing.T) {
	types := testTypes()
	treeUsage := types[PgNodeTree].Usage()
	if !treeUsage.Valid() || treeUsage.Collation.ID != 100 {
		t.Fatal("private tree default usage", treeUsage)
	}
	treeUsage.Collation = CollationRef{ID: 950, Derivation: Default}
	if !treeUsage.Valid() {
		t.Fatal("private tree catalog C usage", treeUsage)
	}
	for _, ref := range []CollationRef{{}, {ID: 950}, {ID: 999999, Derivation: Default}} {
		bad := treeUsage
		bad.Collation = ref
		if bad.Valid() {
			t.Fatal("private tree accepted invalid collation authority", ref)
		}
	}
	if _, err := types[PgNodeTree].Input(t.Context(), "{SQLVALUEFUNCTION}", -1); err == nil {
		t.Fatal("catalog collation enabled private tree input")
	}
	for _, c := range []struct {
		id   ID
		want PGTypeMetadata
	}{
		{16, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{1, true, "c", "p"}, Kind: "b", Category: "B", Preferred: true, Input: 1242, Output: 1243, Receive: 2436, Send: 2437, Modifier: -1}},
		{20, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{8, true, "d", "p"}, Kind: "b", Category: "N", Input: 460, Output: 461, Receive: 2408, Send: 2409, Modifier: -1}},
		{Int2Vector, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "p"}, Kind: "b", Category: "A", Element: Int2, Subscript: 6179, Input: 40, Output: 41, Receive: 2410, Send: 2411, Modifier: -1}},
		{OidVector, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "p"}, Kind: "b", Category: "A", Element: Oid, Subscript: 6179, Input: 54, Output: 55, Receive: 2420, Send: 2421, Modifier: -1}},
		{1006, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "x"}, Kind: "b", Category: "A", Element: Int2Vector, Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1}},
		{1013, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "x"}, Kind: "b", Category: "A", Element: OidVector, Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1}},
		{23, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{4, true, "i", "p"}, Kind: "b", Category: "N", Input: 42, Output: 43, Receive: 2406, Send: 2407, Modifier: -1}},
		{25, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "x"}, Kind: "b", Category: "S", Preferred: true, Input: 46, Output: 47, Receive: 2414, Send: 2415, Modifier: -1, Collation: 100}},
		{XML, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "x"}, Kind: "b", Category: "U", Input: 2893, Output: 2894, Receive: 2898, Send: 2899, Modifier: -1}},
		{143, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "i", "x"}, Kind: "b", Category: "A", Element: XML, Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1}},
		{Point, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{16, false, "d", "p"}, Kind: "b", Category: "G", Element: Float8, Subscript: 6180, Input: 117, Output: 118, Receive: 2428, Send: 2429, Modifier: -1}},
		{Box, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{32, false, "d", "p"}, Kind: "b", Category: "G", Element: Point, Subscript: 6180, Input: 123, Output: 124, Receive: 2484, Send: 2485, Modifier: -1}},
		{1017, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "d", "x"}, Kind: "b", Category: "A", Element: Point, Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1}},
		{1020, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{-1, false, "d", "x"}, Kind: "b", Category: "A", Element: Box, Subscript: 6179, Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1}},
		{2276, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{4, true, "i", "p"}, Kind: "p", Category: "P", Input: 2294, Output: 2295, Modifier: -1}},
		{2281, PGTypeMetadata{PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{8, true, "d", "p"}, Kind: "p", Category: "P", Input: 2304, Output: 2305, Modifier: -1}},
	} {
		got, ok := types[c.id].PGTypeMetadata()
		if !ok || got != c.want {
			t.Fatal(c.id, got, ok, c.want)
		}
	}
	for _, id := range []ID{2201, 4533, 4534, 4535, 6152, 6153, 6155} {
		if got, ok := types[id].PGTypeMetadata(); ok {
			t.Fatal("unsupported family metadata", id, got)
		}
	}
	for _, id := range []ID{143, 1017, 1020, 1006, 1013} {
		if types[id].Concrete() {
			t.Fatal("metadata-only companion enabled an array codec", id)
		}
		if _, err := types[id].Input(t.Context(), "{}", -1); err == nil {
			t.Fatal("metadata-only companion input became executable", id)
		}
	}
	name, _ := types[19].PGTypeMetadata()
	if name.Element != 18 || name.Subscript != 6180 || name.Length != 64 || name.Collation != 950 {
		t.Fatal(name)
	}
	jsonb, _ := types[3802].PGTypeMetadata()
	if jsonb.Element != 0 || jsonb.Subscript != 6098 {
		t.Fatal(jsonb)
	}
	varcharArray, ok := types[1015].PGTypeMetadata()
	if !ok || varcharArray.ModIn != 2915 || varcharArray.ModOut != 2916 || varcharArray.Subscript != 6179 || varcharArray.Element != 1043 || varcharArray.Category != "A" {
		t.Fatal(varcharArray, ok)
	}
	record, ok := types[2287].PGTypeMetadata()
	if !ok || record.Kind != "p" || record.Category != "P" || record.Element != 2249 || record.Alignment != "d" {
		t.Fatal(record, ok)
	}
	acl, _ := types[1033].PGTypeMetadata()
	if acl.Receive != 0 || acl.Send != 0 {
		t.Fatal(acl)
	}
	usage := types[1700].Usage()
	usage.Typmod = 327686
	domain, err := Domain(200000, "number", "number", usage)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Domain(200001, "nested", "nested", domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := nested.PGTypeMetadata()
	if !ok || f.Base != 200000 || f.Modifier != -1 || f.Input != 2597 || f.Receive != 2598 || f.Output != 1702 || f.Send != 2461 || f.Alignment != "i" || f.Storage != "m" || f.Category != "N" {
		t.Fatal(f, ok)
	}
	array := LookupArray(200002, "_nested", nested)
	f, ok = array.PGTypeMetadata()
	if !ok || f.ModIn != 0 || f.ModOut != 0 || f.Element != 200001 || f.Base != 0 {
		t.Fatal(f, ok)
	}
	seed, ok := types[13254].PGTypeMetadata()
	if !ok || !seed.HasDefaultTree || !seed.HasDefault || seed.DefaultTree != "{SQLVALUEFUNCTION :op 4 :type 1184 :typmod 2 :location -1}" || seed.Default != "CURRENT_TIMESTAMP(2)" {
		t.Fatal(seed, ok)
	}
	// Metadata admission must not turn a lookup-only companion into a codec.
	if types[791].Concrete() {
		t.Fatal("money array codec admission widened")
	}
	if _, err := types[791].Input(t.Context(), "{1}", -1); err == nil {
		t.Fatal("lookup-only input became executable")
	}
}

func TestPGTypeDomainComposition(t *testing.T) {
	types := testTypes()
	usage := types[1700].Usage()
	usage.Typmod = 327686
	domain, err := Domain(200000, "number", "number", usage)
	if err != nil {
		t.Fatal(err)
	}
	complete, ok := types[1700].PGTypeMetadata()
	if !ok {
		t.Fatal("missing numeric family facts")
	}
	// This pure composition seam must inherit supplied complete facts rather
	// than recursively replace them with static descriptor facts. Real owned
	// installation addresses are covered by engine and reference witnesses.
	complete.Output, complete.Send, complete.Analyze = 900001, 900002, 900003
	want := PGTypeMetadata{
		PostgreSQLAttributeFacts: PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "m"},
		Kind:                     "d", Category: "N", Base: 1700,
		Input: 2597, Output: 900001, Receive: 2598, Send: 900002, Analyze: 900003,
		Modifier: 327686,
	}
	got := domain.PGTypeDomainMetadata(complete)
	if got != want {
		t.Fatal("complete immediate-base composition", got, want)
	}
	nested, err := Domain(200001, "nested", "nested", domain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	want.Base, want.Modifier = 200000, -1
	if got := nested.PGTypeDomainMetadata(got); got != want {
		t.Fatal("nested immediate-base composition", got, want)
	}
}
