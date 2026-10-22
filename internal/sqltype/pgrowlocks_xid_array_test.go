package sqltype

import (
	"context"
	"testing"
)

// pgrowlocks' documented xids output needs the genuine companion descriptor,
// not an OID-labeled text array or a private serialization path.
func TestPgrowlocksXIDArray(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	array := types[1011]
	if !array.Concrete() || !array.Usage().Valid() || array.Element() != types[Xid] || types[Xid].AssociatedArray() != 1011 {
		t.Fatal("XID companion authority", array)
	}
	first, err := XIDValue(types[Xid], 3)
	if err != nil {
		t.Fatal(err)
	}
	last, err := XIDValue(types[Xid], 4294967295)
	if err != nil {
		t.Fatal(err)
	}
	input := []Value{first, last, Null(types[Xid])}
	value, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 3}}, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = last
	xid, err := value.ArrayElement(0).XID()
	if err != nil || xid != 3 || value.ArrayLen() != 3 || !value.ArrayElement(2).IsNull() {
		t.Fatal("owned XID elements", value, err)
	}
	text, err := array.Output(context.Background(), value)
	if err != nil || text != "{3,4294967295,NULL}" {
		t.Fatal(text, err)
	}
	decoded, err := array.Input(context.Background(), text, -1)
	if err != nil || decoded.Type() != array || decoded.ArrayLen() != 3 || !decoded.ArrayElement(2).IsNull() {
		t.Fatal("shared array codec", decoded, err)
	}
	for i, want := range []uint32{3, 4294967295} {
		got, err := decoded.ArrayElement(i).XID()
		if err != nil || got != want {
			t.Fatal(i, got, err)
		}
	}
	owned, err := OwnedArrayValue(array, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{first, last})
	if err != nil || owned.Type() != array || owned.ArrayLen() != 2 {
		t.Fatal("materialized producer ownership", owned, err)
	}
	empty, err := OwnedArrayValue(array, nil, nil)
	if err != nil || empty.ArrayLen() != 0 {
		t.Fatal("typed empty XID array", empty, err)
	}
	alien, err := XIDValue(foreign[Xid], 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OwnedArrayValue(array, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{alien}); err == nil {
		t.Fatal("foreign element generation admitted")
	}
	for _, id := range []ID{1012, 271} {
		if types[id].Concrete() {
			t.Fatal("adjacent transaction companion enabled", id)
		}
	}
}
