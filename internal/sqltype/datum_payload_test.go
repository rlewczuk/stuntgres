package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestValueSamePayload(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	one := Value{typ: types[Int4], integer: 1}
	retagged := Value{typ: foreign[Int4], integer: 1}
	arrayType, err := Array(1007, "_int4", types[Int4])
	if err != nil {
		t.Fatal(err)
	}
	reownedArrayType, err := Array(1007, "_int4", foreign[Int4])
	if err != nil {
		t.Fatal(err)
	}
	array := Value{typ: arrayType, array: &arrayPayload{dimensions: []ArrayDimension{{Lower: 1, Length: 2}}, elements: []Value{one, Null(types[Int4])}}}
	reownedArray := Value{typ: reownedArrayType, array: &arrayPayload{dimensions: []ArrayDimension{{Lower: 1, Length: 2}}, elements: []Value{retagged, Null(foreign[Int4])}}}
	changedArray := reownedArray
	changedArray.array = &arrayPayload{dimensions: reownedArray.array.dimensions, elements: []Value{{typ: foreign[Int4], integer: 2}, Null(foreign[Int4])}}
	recordType := compositeForTest(t, 50000, CompositeMember{Attribute: 1, Name: "items", Usage: arrayType.Usage()}, CompositeMember{Attribute: 2, Name: "k", Usage: types[Int4].Usage()})
	reownedRecordType := compositeForTest(t, 60000, CompositeMember{Attribute: 1, Name: "items", Usage: reownedArrayType.Usage()}, CompositeMember{Attribute: 2, Name: "k", Usage: foreign[Int4].Usage()})
	record := Value{typ: recordType, composite: &compositePayload{fields: []Value{array, one}}}
	reownedRecord := Value{typ: reownedRecordType, composite: &compositePayload{fields: []Value{reownedArray, retagged}}}
	changedRecord := Value{typ: reownedRecord.typ, composite: &compositePayload{fields: []Value{changedArray, retagged}}}
	enumType, err := EnumWithMembers(45000, "mood", "mood", []EnumMember{{ID: 1, Label: "old", SortOrder: 1}})
	if err != nil {
		t.Fatal(err)
	}
	reownedEnumType, err := EnumWithMembers(45001, "mood", "mood", []EnumMember{{ID: 3, Label: "old", SortOrder: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		a, b Value
		want bool
	}{
		{"descriptor_only", one, retagged, true},
		{"changed_scalar", one, Value{typ: foreign[Int4], integer: 2}, false},
		{"typed_null", Null(types[Int4]), Null(foreign[Int4]), true},
		{"null_change", one, Null(foreign[Int4]), false},
		{"oid_bits", Value{typ: types[Regclass], integer: 1, oid: 42}, Value{typ: foreign[Regclass], integer: 1, oid: 43}, false},
		{"enum_member_bits", Value{typ: enumType, integer: 1}, Value{typ: reownedEnumType, integer: 3}, false},
		{"same_nan_bits", Value{typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000000)}, Value{typ: foreign[Float8], floating: math.Float64frombits(0x7ff8000000000000)}, true},
		{"changed_nan_bits", Value{typ: types[Float8], floating: math.Float64frombits(0x7ff8000000000000)}, Value{typ: foreign[Float8], floating: math.Float64frombits(0x7ff8000000000001)}, false},
		{"array_retag", array, reownedArray, true},
		{"array_cell", array, changedArray, false},
		{"composite_retag", record, reownedRecord, true},
		{"composite_cell", record, changedRecord, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.a.SamePayload(context.Background(), c.b)
			if err != nil || got != c.want {
				t.Fatal(got, c.want, err)
			}
		})
	}
	ctx := &groupingCancelContext{Context: context.Background(), at: 2}
	if _, err := record.SamePayload(ctx, reownedRecord); !errors.Is(err, context.Canceled) {
		t.Fatal("recursive payload comparison ignored cancellation", err)
	}
}
