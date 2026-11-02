package enginetests_test

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

var textPrivateCases = []networkPrivateCase{
	{"text", "", "ab", "b", "missing", 25, 25, 360, [5]ids.OID{664, 665, 98, 667, 666}},
	{"varchar", "", "ab", "b", "missing", 1043, 25, 360, [5]ids.OID{664, 665, 98, 667, 666}},
	{"bpchar", "", "ab", "b", "missing", 1042, 1042, 1078, [5]ids.OID{1058, 1059, 1054, 1061, 1060}},
	{"char", "", "b", `\377`, "z", 18, 18, 358, [5]ids.OID{631, 632, 92, 634, 633}},
	{"name", "", "ab", "b", "missing", 19, 19, 359, [5]ids.OID{660, 661, 93, 663, 662}},
	{"bytea", `\x`, `\x80`, `\xff`, `\x81`, 17, 17, 1954, [5]ids.OID{1957, 1958, 1955, 1960, 1959}},
}

func textPrivateType(tc networkPrivateCase) string {
	if tc.typ == "char" {
		return `"char"`
	}
	return tc.typ
}
func textPrivateInsert(id int, boundary bool) string {
	values := []string{fmt.Sprint(id)}
	for _, tc := range textPrivateCases {
		value := tc.lower
		if boundary {
			value = tc.boundary
		}
		values = append(values, "'"+value+"'")
	}
	return "INSERT INTO text_private VALUES(" + strings.Join(values, ",") + ")"
}
func textEngine(t *testing.T, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	columns := []string{"id integer"}
	var keys []string
	for i, tc := range textPrivateCases {
		columns = append(columns, fmt.Sprintf("c%d %s", i, textPrivateType(tc)))
		keys = append(keys, fmt.Sprintf("c%d %s_ops", i, tc.typ))
	}
	for _, q := range []string{"CREATE EXTENSION btree_gin VERSION '1.3'", "CREATE TABLE text_private(" + strings.Join(columns, ",") + ")", "CREATE INDEX text_private_i ON text_private USING gin(" + strings.Join(keys, ",") + ")"} {
		enginetest.Run(t, e, q)
	}
	if populated {
		for row := range 5 {
			values := []string{fmt.Sprint(row + 1)}
			for _, tc := range textPrivateCases {
				value := tc.lower
				if row == 1 || row == 2 {
					value = tc.boundary
				}
				if row == 3 {
					value = tc.upper
				}
				literal := "'" + value + "'"
				if row == 4 {
					literal = "NULL"
				}
				values = append(values, literal)
			}
			enginetest.Run(t, e, "INSERT INTO text_private VALUES("+strings.Join(values, ",")+")")
		}
	}
	idx, root := ginInstalledRoot(t, e, "text_private_i")
	return e, idx, root
}

func textPrivateTexts(tc networkPrivateCase) []string {
	switch tc.typ {
	case "bpchar":
		return []string{"", "a", "ab", "ab ", "ab  ", "b"}
	case "char":
		return []string{"", "a", "b", "b", `\177`, `\200`, `\377`}
	case "bytea":
		return []string{`\x`, `\x00`, `\x0000`, `\x7f`, `\x80`, `\xff`, `\xff`}
	default:
		return []string{"", "a", "ab", "ab", "ab ", "b"}
	}
}
func textPrivateKeys(tc networkPrivateCase) []scalarDictionaryKey {
	texts := textPrivateTexts(tc)
	groups := [][]int{{0}, {1}, {2, 3}, {4}, {5}}
	if tc.typ == "bpchar" {
		groups = [][]int{{0}, {1}, {2, 3, 4}, {5}}
	}
	if tc.typ == "char" {
		groups = [][]int{{0}, {1}, {2, 3}, {4}, {5}, {6}}
	}
	if tc.typ == "bytea" {
		groups = [][]int{{0}, {1}, {2}, {3}, {4}, {5, 6}}
	}
	var keys []scalarDictionaryKey
	for _, group := range groups {
		keys = append(keys, scalarDictionaryKey{texts[group[0]], group})
	}
	return keys
}
func textPhysical(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Relation, *storage.Relation, *storage.IndexRoot) {
	t.Helper()
	e, idx, _ := textEngine(t, false)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	integer, _ := view.LookupType(sqltype.Int4)
	store := &storage.Relation{}
	for row := range 8 {
		id, _ := sqltype.Integer(integer, int64(row+1))
		values := []sqltype.Value{id}
		for col, tc := range textPrivateCases {
			typ := parent.Columns[col+1].Usage.Type
			v := sqltype.Null(typ)
			texts := textPrivateTexts(tc)
			if row < len(texts) {
				v = ginInput(t, typ, texts[row])
			}
			values = append(values, v)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + row), Version: ids.VersionID(201 + row), Born: 1, Values: values})
	}
	root := networkBuild(t, view, parent, idx, store)
	ginAssertOwners(t, root, store)
	return e, idx, parent, store, root
}

func TestBtreeGINTextBinding(t *testing.T) {
	e, idx, root := textEngine(t, false)
	foreign, other, _ := textEngine(t, false)
	ginFamilyBindingProof(t, e, idx, root, foreign, other, textPrivateCases)
	view := e.CatalogRead()
	for _, slot := range []int{2, 3, 5} {
		a, b := idx.SQLIndex.Keys[0].Class.Support(slot), idx.SQLIndex.Keys[1].Class.Support(slot)
		if a.Routine != b.Routine || a.Member == b.Member || a.Left != 25 || b.Left != 1043 {
			t.Fatal("shared routine must retain distinct associated members", slot, a, b)
		}
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(b.Routine)})
		want := map[int]catalog.ProcedureSignature{2: "25,2281", 3: "25,2281,21,2281,2281", 5: "25,25,21,2281"}[slot]
		if p.Signature != want {
			t.Fatal("varchar helper is not actually text-formal", p.Signature, want)
		}
	}
	for _, q := range []string{"CREATE INDEX text_bad ON text_private USING gin(c2 text_ops)", "CREATE INDEX text_bad ON text_private USING gin(c0 text_ops,c5 text_ops)", "CREATE INDEX text_bad ON text_private USING gin(c4 text_ops)", "CREATE INDEX text_bad ON text_private USING gin(c3 bpchar_ops)"} {
		before := e.CatalogRead().State()
		old := e.Storage.IndexRoots[idx.Address()]
		if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err == nil {
			t.Fatal("functional cast admitted", q)
		}
		if before != e.CatalogRead().State() || old != e.Storage.IndexRoots[idx.Address()] {
			t.Fatal("failed binding published state")
		}
	}
	for _, q := range []string{"CREATE INDEX text_cross_v ON text_private USING gin(c0 varchar_ops)", "CREATE INDEX text_cross_t ON text_private USING gin(c1 text_ops)", "CREATE INDEX text_cross_b ON text_private USING gin(c0 bpchar_ops,c1 bpchar_ops)"} {
		enginetest.Run(t, e, q)
	}
}
