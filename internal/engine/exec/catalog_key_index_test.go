package exec

import (
	"slices"
	"sync"
	"testing"

	"stuntgres/internal/sqltype"
)

func catalogKeyTestTypes(t *testing.T) (oid, text *sqltype.Descriptor) {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Oid:
			oid = d
		case sqltype.Text:
			text = d
		}
	}
	if oid == nil || text == nil {
		t.Fatal("missing builtin oid or text descriptor")
	}
	return oid, text
}

func catalogKeyTestRows(t *testing.T, oid, text *sqltype.Descriptor, keys ...int64) [][]sqltype.Value {
	t.Helper()
	rows := make([][]sqltype.Value, len(keys))
	for i, key := range keys {
		cell := sqltype.Null(oid)
		if key >= 0 {
			var err error
			if cell, err = sqltype.OIDValue(oid, uint32(key)); err != nil {
				t.Fatal(err)
			}
		}
		rows[i] = []sqltype.Value{cell, sqltype.String(text, "row")}
	}
	return rows
}

func TestCatalogKeyIndexPositions(t *testing.T) {
	oid, text := catalogKeyTestTypes(t)
	rows := catalogKeyTestRows(t, oid, text, 7, 3, 7, -1, 5, 7)
	index := NewCatalogKeyIndex(rows)
	column := index.column(0)
	if column.typ != oid {
		t.Fatal("uniform oid column is not indexable")
	}
	for _, tc := range []struct {
		key  uint32
		want []int
	}{{7, []int{0, 2, 5}}, {3, []int{1}}, {5, []int{4}}, {4, []int{}}, {0, []int{}}, {8, []int{}}} {
		if got := column.positions(tc.key); !slices.Equal(got, tc.want) || got == nil {
			t.Fatalf("positions(%d) = %v, want %v", tc.key, got, tc.want)
		}
	}
	if index.column(1).typ != nil || index.column(2) != nil || index.column(-1) != nil {
		t.Fatal("non-oid or absent columns must not be indexable")
	}
}

func TestCatalogKeyIndexRejectsMixedDescriptors(t *testing.T) {
	oid, text := catalogKeyTestTypes(t)
	other, _ := catalogKeyTestTypes(t)
	rows := catalogKeyTestRows(t, oid, text, 1, 2)
	rows = append(rows, catalogKeyTestRows(t, other, text, 3)...)
	if NewCatalogKeyIndex(rows).column(0).typ != nil {
		t.Fatal("a column with two oid descriptors must keep the full scan")
	}
	nulls := catalogKeyTestRows(t, oid, text, 1)
	nulls = append(nulls, []sqltype.Value{sqltype.Null(other), sqltype.String(text, "row")})
	if NewCatalogKeyIndex(nulls).column(0).typ != nil {
		t.Fatal("a NULL cell with another descriptor must keep the full scan")
	}
}

func TestCatalogKeyIndexCoversOnlyItsRows(t *testing.T) {
	oid, text := catalogKeyTestTypes(t)
	rows := catalogKeyTestRows(t, oid, text, 1, 2, 3)
	index := NewCatalogKeyIndex(rows)
	if !index.covers(rows) {
		t.Fatal("index must cover its own rows")
	}
	if index.covers(rows[:2]) || index.covers(slices.Clone(rows)) || index.covers(nil) {
		t.Fatal("index must not cover other row slices")
	}
	var absent *CatalogKeyIndex
	if absent.covers(rows) || NewCatalogKeyIndex(nil).covers(nil) {
		t.Fatal("absent or empty indexes cover nothing")
	}
}

func TestCatalogKeyIndexConcurrentBuild(t *testing.T) {
	oid, text := catalogKeyTestTypes(t)
	index := NewCatalogKeyIndex(catalogKeyTestRows(t, oid, text, 4, 2, 4))
	const readers = 8
	got := make([]*catalogKeyColumn, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() { got[i] = index.column(0) })
	}
	wg.Wait()
	for _, column := range got {
		if column != got[0] || !slices.Equal(column.positions(4), []int{0, 2}) {
			t.Fatal("concurrent builds must publish one column")
		}
	}
}
