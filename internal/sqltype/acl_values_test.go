package sqltype

import (
	"context"
	"testing"
)

func TestACLValuesEmptyArray(t *testing.T) {
	types, next := testTypes(), testTypes()
	d := types[ACLItemArray]
	empty, err := EmptyACLArrayValue(d)
	if err != nil || empty.IsNull() || empty.ArrayRank() != 1 || empty.ArrayDimension(0) != (ArrayDimension{Lower: 1, Length: 0}) || empty.ArrayLen() != 0 {
		t.Fatal("empty ACL shape", empty, err)
	}
	if _, err := ArrayValue(d, []ArrayDimension{{Lower: 1, Length: 0}}, nil); err == nil {
		t.Fatal("generic constructor admitted zero extent")
	}
	generic, err := d.Input(context.Background(), "{}", -1)
	if err != nil || generic.ArrayRank() != 0 {
		t.Fatal("text input normalized incorrectly", err)
	}
	dims := empty.ArrayDimensions()
	dims[0].Length = 99
	if empty.ArrayDimension(0).Length != 0 {
		t.Fatal("dimension alias escaped")
	}
	mapped, err := empty.MapArray(next[ACLItemArray], func(Value, *Descriptor) (Value, error) {
		t.Fatal("empty mapping visited an element")
		return Value{}, nil
	})
	if err != nil || mapped.Type() != next[ACLItemArray] || mapped.ArrayRank() != 1 || mapped.ArrayDimension(0).Length != 0 {
		t.Fatal("mapping lost empty shape", err)
	}
	retagged, err := empty.Retag(next[ACLItemArray])
	if err != nil || retagged.ArrayRank() != 1 || retagged.ArrayDimension(0).Length != 0 {
		t.Fatal("retag lost empty shape", err)
	}
	if _, err := EmptyACLArrayValue(types[1007]); err == nil {
		t.Fatal("non-ACL empty producer accepted")
	}
	text, err := d.Output(context.Background(), empty)
	if err != nil || text != "{}" {
		t.Fatal("empty output", text, err)
	}
}
