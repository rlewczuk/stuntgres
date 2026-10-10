package sqltype

import (
	"context"
	"testing"
)

func TestArrayDimensionAccessors(t *testing.T) {
	d := testTypes()[1007]
	v, err := d.Input(context.Background(), "[-1:0][3:4]={{1,NULL},{2,3}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	if v.ArrayRank() != 2 || v.ArrayLen() != 4 {
		t.Fatal("array rank/elements")
	}
	for i, want := range []ArrayDimension{{Lower: -1, Length: 2}, {Lower: 3, Length: 2}} {
		if got := v.ArrayDimension(i); got != want {
			t.Fatal(i, got, want)
		}
		copy := v.ArrayDimension(i)
		copy.Lower = 99
		if v.ArrayDimension(i) != want {
			t.Fatal("indexed dimension exposed mutable alias")
		}
	}
	owned := v.ArrayDimensions()
	owned[0].Lower = 100
	if v.ArrayDimension(0).Lower != -1 {
		t.Fatal("cloned dimensions mutated immutable value")
	}
	empty, err := d.Input(context.Background(), "{}", -1)
	if err != nil || empty.ArrayRank() != 0 || Null(d).ArrayRank() != 0 || (Value{}).ArrayRank() != 0 {
		t.Fatal("empty/null/zero rank", err)
	}
}
