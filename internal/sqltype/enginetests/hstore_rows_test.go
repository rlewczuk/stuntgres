package enginetests

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"stuntgres/internal/sqltype"
)

func TestHstoreRowsTyped(t *testing.T) {
	ctx := context.Background()
	h := hstoreUnitType(t, 65000)
	text, array, _ := hstoreConstructBuiltins()
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "key", Usage: text.Usage()}, {Attribute: 2, Name: "value", Usage: text.Usage()}})
	if err != nil { t.Fatal(err) }
	input := func(s string) sqltype.Value {
		t.Helper()
		v, err := h.Input(ctx, s, -1)
		if err != nil { t.Fatal(err) }
		return v
	}
	value := input(`a=>NULL,b=>x,c=>x`)
	backing, err := value.HstoreArray(ctx, h, array, false)
	if err != nil { t.Fatal(err) }
	for _, kind := range []sqltype.HstoreRowsKind{sqltype.HstoreRowKeys, sqltype.HstoreRowValues, sqltype.HstoreRowPairs} {
		result := text
		if kind == sqltype.HstoreRowPairs { result = shape }
		plan, err := sqltype.BindHstoreRows(kind, h.Usage(), result.Usage())
		if err != nil { t.Fatal(err) }
		cursor, err := plan.Open(ctx, value)
		if err != nil { t.Fatal(err) }
		independent, err := plan.Open(ctx, input("z=>q"))
		if err != nil { t.Fatal(err) }
		for i := range 3 {
			got, present, err := cursor.Next(ctx)
			if err != nil || !present || got.Type() != result { t.Fatal("present typed occurrence", i, err) }
			if kind == sqltype.HstoreRowPairs {
				if got.CompositeField(0).Type() != text || got.CompositeField(1).Type() != text || got.CompositeField(1).IsNull() != (i == 0) { t.Fatal("pair shape or NULL") }
			} else if kind == sqltype.HstoreRowValues && i == 0 {
				if !got.IsNull() { t.Fatal("present NULL lost") }
			} else {
				offset := 2*i
				if kind == sqltype.HstoreRowValues { offset++ }
				actual, _ := got.Text()
				original, _ := backing.ArrayElement(offset).Text()
				if unsafe.StringData(actual) != unsafe.StringData(original) { t.Fatal("copied immutable text backing") }
			}
		}
		for range 2 {
			got, present, err := cursor.Next(ctx)
			if err != nil || present || !got.IsNull() || got.Type() != result { t.Fatal("typed EOF", err) }
		}
		cursor.Close(); cursor.Close()
		if _, present, err := cursor.Next(ctx); err != nil || present { t.Fatal("closed cursor", err) }
		if _, present, err := independent.Next(ctx); err != nil || !present { t.Fatal("cursor state shared", err) }
		independent.Close()
		for _, empty := range []sqltype.Value{input(""), sqltype.Null(h)} {
			c, err := plan.Open(ctx, empty)
			if err != nil { t.Fatal(err) }
			got, present, err := c.Next(ctx)
			if err != nil || present || !got.IsNull() || got.Type() != result { t.Fatal("empty/NULL EOF", err) }
			c.Close()
		}
		foreign := hstoreUnitType(t, 65000)
		foreignValue, err := foreign.Input(ctx, "a=>x", -1)
		if err != nil { t.Fatal(err) }
		if _, err := plan.Open(ctx, foreignValue); err == nil { t.Fatal("equal-OID foreign generation accepted") }
		canceled, cancel := context.WithCancel(ctx); cancel()
		if _, err := plan.Open(canceled, value); !errors.Is(err, context.Canceled) { t.Fatal("Open cancellation", err) }
		c, err := plan.Open(ctx, value)
		if err != nil { t.Fatal(err) }
		if _, _, err := c.Next(canceled); !errors.Is(err, context.Canceled) { t.Fatal("Next cancellation", err) }
		if _, present, err := c.Next(ctx); err != nil || present { t.Fatal("cancellation retained payload", err) }
	}
	bad, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "value", Usage: text.Usage()}, {Attribute: 2, Name: "key", Usage: text.Usage()}})
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct { kind sqltype.HstoreRowsKind; input, result sqltype.Usage }{
		{99, h.Usage(), text.Usage()}, {sqltype.HstoreRowKeys, text.Usage(), text.Usage()}, {sqltype.HstoreRowPairs, h.Usage(), bad.Usage()}, {sqltype.HstoreRowValues, h.Usage(), array.Usage()},
	} {
		if _, err := sqltype.BindHstoreRows(tc.kind, tc.input, tc.result); err == nil { t.Fatal("invalid plan accepted") }
	}
}
