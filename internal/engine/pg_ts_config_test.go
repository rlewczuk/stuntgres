package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGTSConfigCapture(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigOID}
	source := view.MustRelation(address)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: engineCatalogRuntime{}}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("truthful empty configuration inventory", rows, err)
	}
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
	input.Ref = ref
	captured, err := captureCatalogSourceRows(input, produce)
	if err != nil || captured == nil || captured.Ref != ref || captured.Anonymous == nil || len(captured.Anonymous) != 0 {
		t.Fatal("empty capture lost source identity", captured, err)
	}
	for _, width := range []int{4, 6} {
		invalid := *source
		invalid.Columns = make([]catalog.Column, width)
		copy(invalid.Columns, source.Columns)
		input.Source = &invalid
		if rows, err := produce(input); rows != nil || err == nil {
			t.Fatal("invalid source width admitted", width, rows, err)
		}
	}
	foreign := New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]catalog.Column(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		input.Source = &invalid
		if rows, err := produce(input); rows != nil || err == nil {
			t.Fatal("foreign source type admitted", i, rows, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	// Cancellation takes precedence even over a malformed capture shape.
	invalid := *source
	invalid.Columns = nil
	input.Source = &invalid
	if rows, err := produce(input); rows != nil || err != context.Canceled {
		t.Fatal("canceled producer", rows, err)
	}
	if got, err := captureCatalogSourceRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published source", got, err)
	}
}

func TestPGTSConfigOwnership(t *testing.T) {
	e := New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TSConfigOID}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		relation := view.MustRelation(address)
		if relation.RowType != 10064 || len(relation.Columns) != 5 {
			t.Fatal("native configuration identity/width", relation.RowType, len(relation.Columns))
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10064})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10063})
		ownedRow, rowOK := view.LookupType(10064)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10063 || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native configuration row/array ownership", row, array)
		}
		for i, column := range relation.Columns {
			id := sqltype.Oid
			if i == 1 {
				id = sqltype.Name
			}
			owned, ok := view.LookupType(id)
			if !ok || owned != column.Usage.Type {
				t.Fatal("foreign configuration attribute", i)
			}
		}
		return relation
	}
	oldView := e.CatalogRead()
	old := check(oldView)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldView.LookupType(10064)
	oldArray, _ := oldView.LookupArray(oldRow)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshView := e.CatalogRead()
	fresh := check(freshView)
	if old == fresh || old.Columns[0].Usage.Type == fresh.Columns[0].Usage.Type || old.Columns[1].Usage.Type == fresh.Columns[1].Usage.Type {
		t.Fatal("replacement reused configuration descriptors")
	}
	freshRow, _ := freshView.LookupType(10064)
	freshArray, _ := freshView.LookupArray(freshRow)
	if freshRow == oldRow || freshArray == oldArray {
		t.Fatal("replacement reused configuration row/array owner")
	}
	if !reflect.DeepEqual(old.Columns, columns) || oldArray.Element() != oldRow || check(oldView) != old {
		t.Fatal("replacement changed retained configuration descriptors")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TSConfigOID)
	if !ok {
		t.Fatal("missing configuration producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: freshView, Source: fresh, Owner: freshView.Owner(), Runtime: engineCatalogRuntime{}}
	if rows, err := produce(input); rows == nil || len(rows) != 0 || err != nil {
		t.Fatal("replacement configuration capture", rows, err)
	}
	input.Source = old
	if rows, err := produce(input); rows != nil || err == nil {
		t.Fatal("old generation configuration source admitted", rows, err)
	}
}
