package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestXMLXPathArrayOwnership(t *testing.T) {
	ctx := context.Background()
	types, foreign := testTypes(), testTypes()
	if !types[143].Concrete() || types[143].Element() != types[XML] || types[XML].AssociatedArray() != 143 {
		t.Fatal("array association")
	}
	source, err := types[XML].Input(ctx, "<r><a>x</a><a>x</a></r>", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err := XMLXPath(ctx, types[143], "/r/a", source, Value{}, false, InputEnvironment{}, OutputEnvironment{})
	if err != nil || !v.ValidResult(types[143].Usage()) || v.ArrayRank() != 1 || v.ArrayDimension(0) != (ArrayDimension{1, 2}) {
		t.Fatal(v, err)
	}
	backing := v.array
	xmlBacking := v.ArrayElement(0).xml
	copied, err := v.Retag(foreign[143])
	if err != nil || !copied.ValidResult(foreign[143].Usage()) || copied.ArrayElement(0).Type() != foreign[XML] || copied.ArrayElement(0).xml != xmlBacking {
		t.Fatal("retag", copied, err)
	}
	same, err := v.MapArray(types[143], func(element Value, d *Descriptor) (Value, error) { return element, nil })
	if err != nil || same.array != backing || !SameDatum(same, v) {
		t.Fatal("identity mapping replaced backing", err)
	}
	for _, path := range []string{"/missing", `""`, "1 div 3"} {
		result, err := XMLXPath(ctx, types[143], path, source, Value{}, false, InputEnvironment{XMLMode: XMLDocument}, OutputEnvironment{FloatPrecisionSet: true, ExtraFloatDigits: -3})
		if err != nil || !result.ValidResult(types[143].Usage()) {
			t.Fatal(path, result, err)
		}
		if path == "/missing" {
			if result.ArrayLen() != 0 || result.ArrayRank() != 0 {
				t.Fatal(result)
			}
		} else {
			if result.ArrayLen() != 1 {
				t.Fatal(result)
			}
			payload, _ := result.ArrayElement(0).XML()
			want := ""
			if path == "1 div 3" {
				want = "0.333333333333"
			}
			if payload.RequestedMode() != XMLContent || payload.Original() != want {
				t.Fatal(payload)
			}
		}
	}
	comparison, err := BindComparison(types[143], types[143], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comparison.EqualValues(ctx, v, v, InputEnvironment{}); err == nil {
		t.Fatal("XML[] gained SQL equality")
	}
	if _, err := comparison.Compare(ctx, v, v, InputEnvironment{}); err == nil {
		t.Fatal("XML[] gained SQL ordering")
	}
	d, err := Domain(99007, "xml_array_domain", "xml_array_domain", types[143].Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := d.Wrap(v)
	if err != nil || !wrapped.ValidResult(d.Usage()) || wrapped.array != backing {
		t.Fatal("domain backing", err)
	}
	cancel, stop := context.WithCancel(ctx)
	stop()
	result, err := XMLXPath(cancel, types[143], "/r", source, Value{}, false, InputEnvironment{}, OutputEnvironment{})
	if !errors.Is(err, context.Canceled) || result.typ != nil || v.array != backing || v.ArrayElement(0).xml != xmlBacking {
		t.Fatal("failed publication changed accepted payload", result, err)
	}
}
