package sqltype

import (
	"context"
	"testing"
)

func TestStatisticsNDistinctTypeAuthority(t *testing.T) {
	types := make(map[ID]*Descriptor)
	other := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	for _, d := range Builtins() {
		other[d.ID()] = d
	}
	row, array := types[10029], types[10028]
	if row.Concrete() || array.Concrete() || !row.IsComposite() || row.CompositeLen() != 31 || !array.IsArray() || array.Element() != row {
		t.Fatal("catalog-only row/array shape")
	}
	names := []string{"starelid", "staattnum", "stainherit", "stanullfrac", "stawidth", "stadistinct", "stakind1", "stakind2", "stakind3", "stakind4", "stakind5", "staop1", "staop2", "staop3", "staop4", "staop5", "stacoll1", "stacoll2", "stacoll3", "stacoll4", "stacoll5", "stanumbers1", "stanumbers2", "stanumbers3", "stanumbers4", "stanumbers5", "stavalues1", "stavalues2", "stavalues3", "stavalues4", "stavalues5"}
	for i, name := range names {
		m := row.CompositeMember(i)
		want := Oid
		switch {
		case i == 1 || i >= 6 && i < 11:
			want = Int2
		case i == 2:
			want = Bool
		case i == 3 || i == 5:
			want = Float4
		case i == 4:
			want = Int4
		case i >= 21 && i < 26:
			want = 1021
		case i >= 26:
			want = 2277
		}
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type != types[want] || m.Usage.Type == other[want] {
			t.Fatal(i, m, want)
		}
	}
	for _, d := range []*Descriptor{row, array} {
		usage := d.Usage()
		if !usage.CatalogNullUsage() || usage.Valid() {
			t.Fatal("bounded result admission", d.Name())
		}
		value := CatalogResultNull(usage)
		if !value.IsNull() || !value.ValidResult(usage) || value.ValidResult(other[d.ID()].Usage()) {
			t.Fatal("NULL authority leaked")
		}
		copied := *d
		if copied.Usage().CatalogNullUsage() {
			t.Fatal("descriptor copy gained self authority")
		}
		if _, err := d.Input(context.Background(), "()", -1); err == nil {
			t.Fatal("non-NULL input admitted")
		}
	}
	member := []CompositeMember{{Attribute: 1, Name: "x", Usage: types[2277].Usage()}}
	if _, err := Composite(48000, "bad", "bad", 48001, member); err == nil {
		t.Fatal("ordinary composite pseudo admission relaxed")
	}
	if _, err := Array(48002, "bad_array", types[2277]); err == nil {
		t.Fatal("ordinary array pseudo admission relaxed")
	}
	var usages [31]Usage
	for i := range usages {
		usages[i] = row.CompositeMember(i).Usage
	}
	unchangedRow, unchangedArray, err := RebindStatisticNullDescriptors(row, array, usages[:])
	if err != nil || unchangedRow != row || unchangedArray != array {
		t.Fatal("unchanged closure lost backing", err)
	}
	usages[4] = other[Int4].Usage()
	reboundRow, reboundArray, err := RebindStatisticNullDescriptors(row, array, usages[:])
	if err != nil || reboundRow == row || reboundArray == array || reboundArray.Element() != reboundRow ||
		!reboundRow.Usage().CatalogNullUsage() || !reboundArray.Usage().CatalogNullUsage() ||
		reboundRow.Concrete() || reboundArray.Concrete() {
		t.Fatal("rebound NULL closure lost authority", err)
	}
	if reboundRow.CompositeMember(4).Usage.Type != other[Int4] || row.CompositeMember(4).Usage.Type != types[Int4] || array.Element() != row {
		t.Fatal("rebind mutated the retained generation or kept the replaced member")
	}
	copiedRow := *row
	if _, _, err := RebindStatisticNullDescriptors(&copiedRow, array, usages[:]); err == nil {
		t.Fatal("copied row minted canonical authority")
	}
	usages[4] = other[Int8].Usage()
	if _, _, err := RebindStatisticNullDescriptors(row, array, usages[:]); err == nil {
		t.Fatal("rebind changed fixed member identity")
	}
}
