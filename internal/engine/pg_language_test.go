package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGLanguageCaptureOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgLanguageOID}
	source := catalog.MustRelation(address)
	checkTypeLinks := func(catalog CatalogRead, relation *relationDescriptor) {
		t.Helper()
		if relation.RowType != 10021 {
			t.Fatal("native row type identity", relation.RowType)
		}
		row := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10021})
		array := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10020})
		ownedRow, rowOK := catalog.LookupType(10021)
		ownedArray, arrayOK := catalog.LookupArray(ownedRow)
		if !rowOK || !arrayOK || ownedRow != row.typ || ownedArray != array.typ || row.array != 10020 || array.typ.Element() != row.typ || row.owningRelation != relation.address || array.owningRelation != relation.address || row.owner != relation.Owner || array.owner != relation.Owner {
			t.Fatal("native row/array ownership and reciprocal linkage", row, array)
		}
	}
	checkTypeLinks(catalog, source)
	ref := e.catalog.domain.Ref(e.catalog.state.epoch, address)
	captured, err := capturePGLanguage(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	if captured.ref != ref || len(captured.anonymous) != 1 || len(captured.anonymous[0]) != len(source.Columns) {
		t.Fatal("capture lost source identity/shape", captured)
	}
	for i, value := range captured.anonymous[0] {
		if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i == 8) {
			t.Fatal("capture lost owned type or typed ACL NULL", i, value)
		}
	}
	// A second capture must not lend mutable row storage from the first.
	second, err := capturePGLanguage(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	second.anonymous[0][0] = sqltype.Null(source.Columns[0].Usage.Type)
	if captured.anonymous[0][0].IsNull() {
		t.Fatal("captures share mutable row slots")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := capturePGLanguage(ctx, catalog, ref, source); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published source", got, err)
	}
	invalid := *source
	invalid.Columns = source.Columns[:8]
	if got, err := capturePGLanguage(context.Background(), catalog, ref, &invalid); got != nil || err == nil {
		t.Fatal("truncated source admitted", got, err)
	}
	foreign := New().CatalogRead()
	for i, column := range source.Columns {
		invalid := *source
		invalid.Columns = append([]ColumnDescriptor(nil), source.Columns...)
		invalid.Columns[i].Usage.Type, _ = foreign.LookupType(column.Usage.Type.ID())
		if got, err := capturePGLanguage(context.Background(), catalog, ref, &invalid); got != nil || err == nil {
			t.Fatal("foreign source type admitted", i, got, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := e.CatalogRead()
	freshSource := fresh.MustRelation(address)
	checkTypeLinks(fresh, freshSource)
	if freshSource.Columns[8].Usage.Type == source.Columns[8].Usage.Type {
		t.Fatal("replacement reused ACL type owner")
	}
	if got, err := capturePGLanguage(context.Background(), fresh, ref, source); got != nil || err == nil {
		t.Fatal("old generation source admitted", got, err)
	}
	for i, value := range captured.anonymous[0] {
		if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i == 8) {
			t.Fatal("replacement changed retained capture", i, value)
		}
	}
}
