package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestPgsurgeryFreezeMaintenanceProjection(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	value, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	old := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: integer.Usage()}}, Attributes: []int32{1}}
	next := &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: integer.Usage()}}, Attributes: []int32{1, 2}}
	row := storage.RowVersion{Row: 1, Version: 1, Values: []sqltype.Value{value}}
	discarded, _ := storage.RetainDiscardedVersions(nil, []storage.RowVersion{row}, nil)
	headers, err := storage.RepairHeapHeaders(nil, []storage.RowVersion{row})
	if err != nil {
		t.Fatal(err)
	}
	store := &storage.Relation{Discarded: discarded, Headers: headers, Generation: 8}
	shape := session.StorageChange{Fills: map[int32]sqltype.Value{2: sqltype.Null(integer)}}
	projected, _, _, err := session.ProjectColumnShapeRoots(context.Background(), false, old, next, store, shape, visibility.Root{}, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	replacement := projected.Replacement
	if replacement == store || replacement.Generation != 9 || replacement.Discarded == discarded || replacement.Headers == headers {
		t.Fatal("projection did not own changed roots")
	}
	if len(replacement.Discarded.Version.Values) != 2 || len(replacement.Headers.Additional[0].Values) != 2 || !replacement.Headers.Additional[0].Values[1].IsNull() {
		t.Fatal("projection omitted actual repaired/discarded payload")
	}
	if replacement.Headers.Header(1) != headers.Header(1) || replacement.Discarded.Version.Version != row.Version {
		t.Fatal("projection changed exact header authority")
	}
	if len(discarded.Version.Values) != 1 || len(headers.Additional[0].Values) != 1 || store.Generation != 8 {
		t.Fatal("projection mutated captured old roots")
	}
	visible := storage.VisibleHeapVersions(replacement, 1)
	if len(visible) != 1 || visible[0].Version != 1 || len(visible[0].Values) != 2 || visible[0].HeapXmin() != 2 {
		t.Fatal("projected repaired data was not visible", visible)
	}
}
