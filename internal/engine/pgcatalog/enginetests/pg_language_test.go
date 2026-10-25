package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGLanguageCaptureOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.LanguageOID}
	source := view.MustRelation(address)
	checkTypeLinks := func(catalog catalog.Read, relation *catalog.Relation) {
		t.Helper()
		if relation.RowType != 10021 {
			t.Fatal("native row type identity", relation.RowType)
		}
		row := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10021})
		array := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10020})
		ownedRow, rowOK := catalog.LookupType(10021)
		ownedArray, arrayOK := catalog.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.SQLType || ownedArray != array.SQLType || row.Array != 10020 || array.SQLType.Element() != row.SQLType || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() || row.Owner != relation.Owner || array.Owner != relation.Owner {
			t.Fatal("native row/array ownership and reciprocal linkage", row, array)
		}
	}
	checkTypeLinks(view, source)
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	captured, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Ref != ref || len(captured.Anonymous) != 4 || len(captured.Anonymous[0]) != len(source.Columns) {
		t.Fatal("capture lost source identity/shape", captured)
	}
	for i, value := range captured.Anonymous[0] {
		if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i == 8) {
			t.Fatal("capture lost owned type or typed ACL NULL", i, value)
		}
	}
	// A second capture must not lend mutable row storage from the first.
	second, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage)
	if err != nil {
		t.Fatal(err)
	}
	second.Anonymous[0][0] = sqltype.Null(source.Columns[0].Usage.Type)
	if captured.Anonymous[0][0].IsNull() {
		t.Fatal("captures share mutable row slots")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Roles: e.Roles(), Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published source", got, err)
	}
	invalid := *source
	invalid.Columns = source.Columns[:8]
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Ref: ref, Source: &invalid, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage); got != nil || err == nil {
		t.Fatal("truncated source admitted", got, err)
	}
	foreign := engine.New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]catalog.Column(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.Roles(), Ref: ref, Source: &invalid, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage); got != nil || err == nil {
			t.Fatal("foreign source type admitted", i, got, err)
		}
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fresh := e.CatalogRead()
	freshSource := fresh.MustRelation(address)
	checkTypeLinks(fresh, freshSource)
	if freshSource.Columns[8].Usage.Type == source.Columns[8].Usage.Type {
		t.Fatal("replacement reused ACL type owner")
	}
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: fresh, Roles: e.Roles(), Ref: ref, Source: source, Owner: (fresh).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGLanguage); got != nil || err == nil {
		t.Fatal("old generation source admitted", got, err)
	}
	for i, value := range captured.Anonymous[0] {
		if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i == 8) {
			t.Fatal("replacement changed retained capture", i, value)
		}
	}
}
