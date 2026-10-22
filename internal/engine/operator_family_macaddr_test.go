package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestOperatorFamilyMacaddrSupportExecution(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Macaddr)
	ctx := context.Background()
	value := func(n uint64) sqltype.Value {
		v, err := sqltype.MACADDRValue(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	class, _ := sqltype.DefaultIndexClass(typ)
	selected, err := routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	want := sqltype.IndexSupport{Family: 1984, Member: 10074, Routine: 836, Left: 829, Right: 829, Revision: 1, FamilyRevision: 1, RoutineRevision: 1}
	if class.OID() != 10024 || selected.Support(1) != want {
		t.Fatal("actual MACADDR class/member/routine", class, selected.Support(1))
	}
	comparison, err := routine.BindIndexOrdering(view, typ.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	// Independently observed pinned18.6 vectors, including unsigned octet order.
	for _, tc := range []struct {
		a, b uint64
		want int
	}{{0, 0, 0}, {0, 1, -1}, {1, 0, 1}, {0x7fffffffffff, 0x800000000000, -1}, {0xffffffffffff, 0, 1}} {
		got, err := comparison.Compare(ctx, value(tc.a), value(tc.b), sqltype.InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal("oracle MACADDR comparator", tc, got, err)
		}
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90020}, ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "m", Usage: typ.Usage()}}, Index: map[string]int{"m": 0}}
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90021}, ObjectRevision: 1, SQLIndex: definition}
	store := &storage.Relation{Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value(0x800000000000)}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value(0x7fffffffffff)}},
		{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{sqltype.Null(typ)}},
		{Row: 4, Version: 4, Born: 1, Values: []sqltype.Value{value(0x800000000000)}},
	}}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, parent, definition, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := func(root *storage.IndexRoot) []ids.RowID {
		var result []ids.RowID
		for _, entry := range root.Entries {
			result = append(result, entry.Row)
			if entry.Version != ids.VersionID(entry.Row) {
				t.Fatal("lost version identity", entry)
			}
		}
		return result
	}
	if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
		t.Fatal("unsigned MACADDR root/duplicates/NULL", rows(root))
	}
	inserted := storage.RowVersion{Row: 5, Version: 5, Values: []sqltype.Value{value(0)}}
	delta := &index.RowPublication{Store: store, Root: store.Versions, Next: append(append([]storage.RowVersion(nil), store.Versions...), inserted), Writes: []storage.RowVersion{inserted}, SingleSlot: -1, Deletion: &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Version: 1, Ordinal: 0}}}}
	next, err := index.MergeIndexRoot(evaluation, parent, idx, root.Entries, delta, false, true, false, "", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil || !reflect.DeepEqual(rows(next), []ids.RowID{5, 2, 4, 3}) {
		t.Fatal("MACADDR insert/remove root", next, err)
	}
	if !reflect.DeepEqual(rows(root), []ids.RowID{2, 1, 4, 3}) {
		t.Fatal("mutated retained MACADDR root")
	}
	if _, ok := sqltype.DefaultHashIndexClass(typ); ok {
		t.Fatal("invented MACADDR hash class")
	}
}
