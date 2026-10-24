package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGTransformCapture(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TransformOID}
	source := view.MustRelation(address)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TransformOID)
	if !ok {
		t.Fatal("missing transform producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: engineCatalogRuntime{}}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("truthful empty transform inventory", rows, err)
	}
	ref := e.catalog.Domain.Ref(e.catalog.State.Epoch(), address)
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
		if rows, err := produce(input); rows != nil || err == nil || err.Error() != "stuntgres: invalid transform source shape" {
			t.Fatal("invalid source width admitted", width, rows, err)
		}
	}
	foreign := New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]catalog.Column(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		input.Source = &invalid
		if rows, err := produce(input); rows != nil || err == nil || err.Error() != "stuntgres: foreign transform source type" {
			t.Fatal("foreign source type admitted", i, rows, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	invalid := *source
	invalid.Columns = nil
	input.Source = &invalid
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation must precede shape admission", rows, err)
	}
	if got, err := captureCatalogSourceRows(input, produce); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published source", got, err)
	}
}

func TestPGTransformOwnership(t *testing.T) {
	e := New()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3576}
	check := func(view catalog.Read) *catalog.Relation {
		t.Helper()
		relation := view.MustRelation(address)
		if relation.Name() != "pg_transform" || relation.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || relation.RelationKind != catalog.CatalogRelation || relation.VirtualView || !relation.History.HasIndexes || relation.RowType != 10103 || len(relation.Columns) != 5 {
			t.Fatal("native transform relation contract", relation)
		}
		owner := relation.Owner
		if owner.ID() != 10 || len(relation.Security.ACL.Entries) != 1 {
			t.Fatal("native transform owner/ACL", relation.Owner, relation.Security)
		}
		grant := relation.Security.ACL.Entries[0]
		if !grant.Grantee.Public || grant.Grantor != owner || grant.Privileges != authorization.SelectPrivilege {
			t.Fatal("native transform PUBLIC SELECT", grant)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10103})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10102})
		ownedRow, rowOK := view.LookupType(10103)
		ownedArray, arrayOK := view.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10102 || array.SQLType.Element() != row.SQLType || row.OwningRelation != address || array.OwningRelation != address || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native transform row/array ownership", row, array)
		}
		for i, name := range []string{"oid", "trftype", "trflang", "trffromsql", "trftosql"} {
			column := relation.Columns[i]
			id := sqltype.Oid
			if i >= 3 {
				id = sqltype.Regproc
			}
			owned, ok := view.LookupType(id)
			if !ok || owned != column.Usage.Type || column.Name != name || column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.DeclaredDimensions != 0 || column.OriginTable != 3576 || column.OriginAttribute != int16(i+1) || relation.Attributes[i] != int32(i+1) {
				t.Fatal("transform attribute contract", i, column)
			}
		}
		return relation
	}
	oldView := e.CatalogRead()
	old := check(oldView)
	columns := append([]catalog.Column(nil), old.Columns...)
	oldRow, _ := oldView.LookupType(10103)
	oldArray, _ := oldView.LookupArray(oldRow)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	freshView := e.CatalogRead()
	fresh := check(freshView)
	if old == fresh {
		t.Fatal("replacement reused transform relation")
	}
	for i := range old.Columns {
		if old.Columns[i].Usage.Type == fresh.Columns[i].Usage.Type {
			t.Fatal("replacement reused transform carrier", i)
		}
	}
	freshRow, _ := freshView.LookupType(10103)
	freshArray, _ := freshView.LookupArray(freshRow)
	if freshRow == oldRow || freshArray == oldArray {
		t.Fatal("replacement reused transform row/array owner")
	}
	if !reflect.DeepEqual(old.Columns, columns) || oldArray.Element() != oldRow || check(oldView) != old {
		t.Fatal("replacement changed retained transform descriptors")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TransformOID)
	if !ok {
		t.Fatal("missing transform producer")
	}
	input := pgcatalog.Input{Context: context.Background(), Catalog: freshView, Source: fresh, Owner: freshView.Owner(), Runtime: engineCatalogRuntime{}}
	if rows, err := produce(input); rows == nil || len(rows) != 0 || err != nil {
		t.Fatal("replacement transform capture", rows, err)
	}
	input.Source = old
	if rows, err := produce(input); rows != nil || err == nil {
		t.Fatal("old generation transform source admitted", rows, err)
	}
}
