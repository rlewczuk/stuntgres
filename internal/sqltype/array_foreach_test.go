package sqltype

import (
	"context"
	"testing"
)

func TestArrayForeachSlice(t *testing.T) {
	d := testTypes()[1007]
	ctx := context.Background()
	v, err := d.Input(ctx, "[0:1][-2:-1][5:6]={{{1,2},{3,NULL}},{{5,6},{7,8}}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		rank, offset, size int
		want               string
	}{
		{1, 0, 2, "[5:6]={1,2}"},
		{1, 2, 2, "[5:6]={3,NULL}"},
		{1, 6, 2, "[5:6]={7,8}"},
		{2, 0, 4, "[-2:-1][5:6]={{1,2},{3,NULL}}"},
		{2, 4, 4, "[-2:-1][5:6]={{5,6},{7,8}}"},
		{3, 0, 8, "[0:1][-2:-1][5:6]={{{1,2},{3,NULL}},{{5,6},{7,8}}}"},
	} {
		slice := v.ArrayForeachSlice(tc.rank, tc.offset, tc.size)
		text, err := d.Output(ctx, slice)
		if err != nil || text != tc.want || slice.ArrayRank() != tc.rank || slice.ArrayLen() != tc.size || slice.Type() != v.Type() {
			t.Fatal("wrong trailing shape/value/generation", tc, text, err)
		}
		if tc.rank == v.ArrayRank() {
			if slice.array != v.array {
				t.Fatal("whole immutable slice copied")
			}
		} else if slice.array == v.array || &slice.array.elements[0] == &v.array.elements[tc.offset] || &slice.array.dimensions[0] == &v.array.dimensions[v.ArrayRank()-tc.rank] {
			t.Fatal("proper slice leaked borrowed buffers")
		}
	}
	first := v.ArrayForeachSlice(1, 0, 2)
	last := v.ArrayForeachSlice(1, 6, 2)
	last.array.elements[0] = Null(d.Element())
	last.array.dimensions[0].Lower = 99
	text, err := d.Output(ctx, first)
	if err != nil || text != "[5:6]={1,2}" || v.ArrayDimension(2).Lower != 5 || v.ArrayElement(6).IsNull() {
		t.Fatal("later delivery changed retained slice/source", text, err)
	}
	nulls, err := d.Input(ctx, "{{NULL,NULL},{NULL,NULL}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < 4; offset += 2 {
		slice := nulls.ArrayForeachSlice(1, offset, 2)
		if slice.IsNull() || slice.ArrayLen() != 2 || !slice.ArrayElement(0).IsNull() || !slice.ArrayElement(1).IsNull() {
			t.Fatal("all-NULL slice lost non-NULL array identity")
		}
	}
	var total int64
	if allocations := testing.AllocsPerRun(100, func() {
		total = 0
		for i := range v.ArrayLen() {
			element := v.ArrayElement(i)
			if !element.IsNull() {
				n, _ := element.Integer()
				total += n
			}
		}
	}); allocations != 0 || total != 32 {
		t.Fatal("element inspection allocated or reordered source", allocations, total)
	}
}
