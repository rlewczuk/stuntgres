package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSearchOccurrenceOrdering(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	record, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "n", Usage: types[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(2287, "_record", record)
	if err != nil {
		t.Fatal(err)
	}
	value := func(n int64) Value {
		scalar, e := Integer(types[Int4], n)
		if e != nil {
			t.Fatal(e)
		}
		v, e := OwnedCompositeValue(record, []Value{scalar})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	two, ten := value(2), value(10)
	null, err := OwnedCompositeValue(record, []Value{Null(types[Int4])})
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := BindOrdering(record.Usage())
	if err != nil || !ordering.OrderingBound() || ordering.DefaultBtreeOpclass() != 0 {
		t.Fatal(ordering, err)
	}
	plain, err := BindComparison(record, record, CollationRef{}, false)
	if err != nil || plain.OrderingBound() || ordering.Equal(plain) {
		t.Fatal("ordering state not distinct", err)
	}
	for _, pair := range [][2]Value{{two, ten}, {ten, null}} {
		if sign, err := ordering.Compare(ctx, pair[0], pair[1], InputEnvironment{}); err != nil || sign >= 0 {
			t.Fatal(sign, err)
		}
	}
	path := func(elements ...Value) Value {
		v, e := OwnedArrayValue(array, []ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	ordering, err = BindOrdering(array.Usage())
	if err != nil || !ordering.OrderingBound() || ordering.DefaultBtreeOpclass() != 0 {
		t.Fatal(ordering, err)
	}
	if sign, err := ordering.Compare(ctx, path(two), path(two, ten), InputEnvironment{}); err != nil || sign >= 0 {
		t.Fatal("ancestry prefix", sign, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ordering.Compare(canceled, path(two), path(ten), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	jsonRecord, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "n", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "j", Usage: types[JSON].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	lazy, err := BindOrdering(jsonRecord.Usage())
	if err != nil {
		t.Fatal("eager JSON admission", err)
	}
	jvalue := func(n int64) Value {
		v, _ := Integer(types[Int4], n)
		record, e := OwnedCompositeValue(jsonRecord, []Value{v, Null(types[JSON])})
		if e != nil {
			t.Fatal(e)
		}
		return record
	}
	if sign, err := lazy.Compare(ctx, jvalue(2), jvalue(10), InputEnvironment{}); err != nil || sign >= 0 {
		t.Fatal("lost short circuit", sign, err)
	}
	_, err = lazy.Compare(ctx, jvalue(2), jvalue(2), InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "could not identify a comparison function for type json" {
		t.Fatal(err)
	}
	if _, ok := DefaultIndexClass(record); ok {
		t.Fatal("record acquired physical index capability")
	}
	if _, err := BindOrdering(types[1007].Usage()); err == nil {
		t.Fatal("general array ordering widened")
	}
	named, err := Composite(48000, "named", "public.named", 48001, []CompositeMember{{Attribute: 1, Name: "n", Usage: types[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindOrdering(named.Usage()); err == nil {
		t.Fatal("named ordering widened")
	}
	scalar, err := BindOrdering(types[Int4].Usage())
	if err != nil || !scalar.OrderingBound() || scalar.DefaultBtreeOpclass() != 1978 {
		t.Fatal(scalar, err)
	}
}
