package enginetests_test

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

var scalarPrivateCases = []networkPrivateCase{
	{"int2", "-32768", "0", "32767", "2", 21, 21, 350, [5]ids.OID{95, 522, 94, 524, 520}},
	{"int4", "-2147483648", "0", "2147483647", "2", 23, 23, 351, [5]ids.OID{97, 523, 96, 525, 521}},
	{"int8", "-9223372036854775808", "0", "9223372036854775807", "2", 20, 20, 842, [5]ids.OID{412, 414, 410, 415, 413}},
	{"float4", "-Infinity", "0", "NaN", "2", 700, 700, 354, [5]ids.OID{622, 624, 620, 625, 623}},
	{"float8", "-Infinity", "0", "NaN", "2", 701, 701, 355, [5]ids.OID{672, 673, 670, 675, 674}},
	{"bool", "false", "false", "true", "", 16, 16, 1693, [5]ids.OID{58, 1694, 91, 1695, 59}},
}

func scalarEngine(t *testing.T, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	return ginFamilyEngine(t, "scalar_private", scalarPrivateCases, populated)
}
func scalarPrivateTexts(tc networkPrivateCase) []string {
	switch tc.typ {
	case "bool":
		return []string{"false", "false", "true", ""}
	case "float4":
		return []string{"-Infinity", "-1", "-0", "0", "1e-45", "1", "Infinity", "NaN", "NaN", "", "1.17549435e-38", "3.4028235e38", "-1e-45", "16777217", "16777218", "0.1", "7.038531e-26"}
	case "float8":
		return []string{"-Infinity", "-1", "-0", "0", "5e-324", "1", "Infinity", "NaN", "NaN", "", "2.2250738585072014e-308", "1.7976931348623157e308", "-5e-324", "1.0000000000000002"}
	case "int8":
		return []string{tc.lower, "-1", "0", "0", "1", tc.upper, "", "9007199254740992", "9007199254740993", "9007199254740994"}
	default:
		return []string{tc.lower, "-1", "0", "0", "1", tc.upper, ""}
	}
}

type scalarDictionaryKey struct {
	text      string
	positions []int
}

func scalarPrivateKeys(tc networkPrivateCase) []scalarDictionaryKey {
	switch tc.typ {
	case "bool":
		return []scalarDictionaryKey{{"false", []int{0, 1}}, {"true", []int{2}}}
	case "float4":
		return []scalarDictionaryKey{{"-Infinity", []int{0}}, {"-1", []int{1}}, {"-1e-45", []int{12}}, {"0", []int{2, 3}}, {"1e-45", []int{4}}, {"1.17549435e-38", []int{10}}, {"7.038531e-26", []int{16}}, {"0.1", []int{15}}, {"1", []int{5}}, {"16777216", []int{13}}, {"16777218", []int{14}}, {"3.4028235e38", []int{11}}, {"Infinity", []int{6}}, {"NaN", []int{7, 8}}}
	case "float8":
		return []scalarDictionaryKey{{"-Infinity", []int{0}}, {"-1", []int{1}}, {"-5e-324", []int{12}}, {"0", []int{2, 3}}, {"5e-324", []int{4}}, {"2.2250738585072014e-308", []int{10}}, {"1", []int{5}}, {"1.0000000000000002", []int{13}}, {"1.7976931348623157e308", []int{11}}, {"Infinity", []int{6}}, {"NaN", []int{7, 8}}}
	case "int8":
		return []scalarDictionaryKey{{tc.lower, []int{0}}, {"-1", []int{1}}, {"0", []int{2, 3}}, {"1", []int{4}}, {"9007199254740992", []int{7}}, {"9007199254740993", []int{8}}, {"9007199254740994", []int{9}}, {tc.upper, []int{5}}}
	default:
		return []scalarDictionaryKey{{tc.lower, []int{0}}, {"-1", []int{1}}, {"0", []int{2, 3}}, {"1", []int{4}}, {tc.upper, []int{5}}}
	}
}
func scalarPhysical(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Relation, *storage.Relation, *storage.IndexRoot) {
	t.Helper()
	e, idx, _ := scalarEngine(t, false)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	integer, _ := view.LookupType(sqltype.Int4)
	store := &storage.Relation{}
	for row := range 17 {
		id, _ := sqltype.Integer(integer, int64(row+1))
		values := []sqltype.Value{id}
		for col, tc := range scalarPrivateCases {
			typ := parent.Columns[col+1].Usage.Type
			value := sqltype.Null(typ)
			texts := scalarPrivateTexts(tc)
			if row < len(texts) && texts[row] != "" {
				value = ginInput(t, typ, texts[row])
			}
			values = append(values, value)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + row), Version: ids.VersionID(201 + row), Born: 1, Values: values})
	}
	root := networkBuild(t, view, parent, idx, store)
	ginAssertOwners(t, root, store)
	return e, idx, parent, store, root
}
func TestBtreeGINScalarBinding(t *testing.T) {
	e, idx, root := scalarEngine(t, false)
	foreign, other, _ := scalarEngine(t, false)
	ginFamilyBindingProof(t, e, idx, root, foreign, other, scalarPrivateCases)
	view := e.CatalogRead()
	for _, pair := range [][2]int{{0, 1}, {1, 2}, {3, 4}, {5, 0}} {
		typ, _ := view.LookupType(sqltype.ID(scalarPrivateCases[pair[0]].input))
		class := idx.SQLIndex.Keys[pair[1]].Class
		if class.Accepts(typ) {
			t.Fatalf("cross-type class admission %v", pair)
		}
		if _, err := sqltype.BindGinTokens(typ.Usage(), class); err == nil {
			t.Fatalf("cross-type binding %v", pair)
		}
	}
	// Failure occurs before a root can be published, including on an empty heap.
	old := e.Storage.IndexRoots[idx.Address()]
	for _, q := range []string{"CREATE INDEX scalar_bad ON scalar_private USING gin(int2 int4_ops)", "CREATE INDEX scalar_bad ON scalar_private USING gin(int4 int4_ops,float4 float8_ops)"} {
		if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err == nil {
			t.Fatal("unavailable class/type accepted", q)
		}
		if e.Storage.IndexRoots[idx.Address()] != old {
			t.Fatal("failed binding changed good root")
		}
	}
	enginetest.Run(t, e, "CREATE INDEX scalar_bad ON scalar_private USING gin(int2 int2_ops)")
}
