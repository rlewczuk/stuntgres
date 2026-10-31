package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGTSConfigMapOwnership(t *testing.T) {
	e := engine.New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigMapOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		relation := view.MustRelation(address)
		if relation.Address() != address || relation.RowType != 10066 || len(relation.Columns) != 4 {
			t.Fatal("native mapping identity/shape", relation)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10066})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10065})
		ownedRow, rowOK := view.LookupType(10066)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10065 || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native mapping row/array ownership", row, array)
		}
		for i, name := range []string{"mapcfg", "maptokentype", "mapseqno", "mapdict"} {
			id := sqltype.Oid
			if i == 1 || i == 2 {
				id = sqltype.Int4
			}
			owned, ok := view.LookupType(id)
			if !ok || relation.Columns[i].Name != name || relation.Columns[i].Usage.Type != owned {
				t.Fatal("mapping source attribute identity", i, relation.Columns[i])
			}
		}
		return relation
	}
	oldView := e.CatalogRead()
	old := check(oldView)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldView.LookupType(10066)
	oldArray, _ := oldView.LookupArray(oldRow)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigMapOID)
	if !ok {
		t.Fatal("missing mapping producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: oldView, Source: old, Owner: oldView.Owner(), Runtime: core.EngineCatalogRuntime{}}
	rows, err := produce(input)
	if err != nil || len(rows) != 399 {
		t.Fatal("old generation mapping rows", rows, err)
	}
	retained := make([][]sqltype.Value, len(rows))
	for i := range rows {
		retained[i] = append([]sqltype.Value(nil), rows[i]...)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	freshView := e.CatalogRead()
	fresh := check(freshView)
	freshRow, _ := freshView.LookupType(10066)
	freshArray, _ := freshView.LookupArray(freshRow)
	if old == fresh || oldRow == freshRow || oldArray == freshArray {
		t.Fatal("replacement reused mapping relation/row/array")
	}
	for i := range columns {
		if columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("replacement reused mapping attribute type", i)
		}
		for _, row := range rows {
			if row[i].Type() != columns[i].Usage.Type || row[i].Type() == fresh.Columns[i].Usage.Type {
				t.Fatal("retained mapping cell retagged", i)
			}
		}
	}
	if !reflect.DeepEqual(old.Columns, columns) || !reflect.DeepEqual(rows, retained) || oldArray.Element() != oldRow || check(oldView) != old {
		t.Fatal("replacement changed retained mapping generation")
	}
	input.Catalog, input.Owner, input.Source = freshView, freshView.Owner(), fresh
	if got, err := produce(input); err != nil || got == nil || len(got) != 399 {
		t.Fatal("fresh mapping producer", got, err)
	}
	input.Source = old
	if got, err := produce(input); got != nil || err == nil {
		t.Fatal("fresh mapping producer admitted old source", got, err)
	}
	if got, err := core.CaptureCatalogSourceRows(input, produce); got != nil || err == nil {
		t.Fatal("wrapper admitted old mapping source", got, err)
	}
}
