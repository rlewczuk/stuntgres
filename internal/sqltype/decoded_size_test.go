package sqltype

import (
	"context"
	"testing"
	"unsafe"
)

func TestLogicalDatumSize(t *testing.T) {
	types := testTypes()
	text := String(types[Text], "wide")
	array, err := ArrayValue(types[1009], []ArrayDimension{{Lower: 1, Length: 2}}, []Value{text, Null(types[Text])})
	if err != nil {
		t.Fatal(err)
	}
	// The logical budget is defined here independently: outer slot 16,
	// dimension 8, text slot+bytes 20, NULL slot 16. No physical-byte claim.
	if got := array.LogicalSize(); got != 60 {
		t.Fatal("structural logical charge", got)
	}
	if text.LogicalSize() != 20 || Null(types[Text]).LogicalSize() != 16 {
		t.Fatal("scalar logical charge")
	}
	json, err := types[JSONB].Input(context.Background(), `{"a":[1,"x"]}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	// Value slot + object node/key + array node + numeric node/digit +
	// string node/byte: 16 + 17 + 16 + 17 + 17. The independent canonical
	// text cache {"a": [1, "x"]} must not charge this logical data twice.
	if got := json.LogicalSize(); got != 83 {
		t.Fatal("JSON logical charge", got)
	}
	jsonPayload := json.jsonb
	nestedBacking := &json.jsonb.root.fields[0].value.items[0]
	before := array
	for range 3 {
		if array.LogicalSize() != 60 || !SameDatum(array, before) {
			t.Fatal("accounting mutated immutable backing")
		}
		if json.LogicalSize() != 83 || json.jsonb != jsonPayload || &json.jsonb.root.fields[0].value.items[0] != nestedBacking {
			t.Fatal("accounting replaced immutable nested JSONB backing")
		}
	}
}

func TestLogicalDatumSizeJSONB(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		name, input, canonical string
		nodes, payloadBytes    int64
	}{
		{"nested", ` { "a" : [ 1, "x" ] } `, `{"a": [1, "x"]}`, 4, 3},
		// JSON escape syntax and UTF-8 encoding are not interchangeable
		// budgets: decoded é occupies two bytes and newline occupies one.
		{"escaped Unicode", `"\u00e9\n"`, `"é\n"`, 1, 3},
		// Numeric owns coefficient "1" plus fixed exponent/scale fields,
		// not seven rendered digits. No output is needed to measure it.
		{"numeric exponent", `1e6`, `1000000`, 1, 1},
		{"mixed array", `[true,null,123,"xy",{},[]]`, `[true, null, 123, "xy", {}, []]`, 7, 5},
		// Only canonical keys a/zz, string long and coefficient 12 remain.
		{"duplicate keys and order", `{"zz":0,"a":[true,null,"long"],"zz":12}`, `{"a": [true, null, "long"], "zz": 12}`, 6, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := types[JSONB].Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := types[JSONB].Input(context.Background(), tc.canonical, -1)
			if err != nil {
				t.Fatal(err)
			}
			// Save the actual accepted owners/backing, not just equal text.
			payload, root := value.jsonb, value.jsonb.root
			textBacking := unsafe.StringData(value.text)
			uncached := value
			uncached.text = ""
			want := 16 + 16*tc.nodes + tc.payloadBytes
			for range 3 {
				if value.LogicalSize() != want || fresh.LogicalSize() != want || uncached.LogicalSize() != want {
					t.Fatal("logical structure/cache charge", value.LogicalSize(), fresh.LogicalSize(), uncached.LogicalSize(), want)
				}
			}
			if value.jsonb != payload || value.text != tc.canonical || unsafe.StringData(value.text) != textBacking {
				t.Fatal("accounting replaced immutable owner or display backing")
			}
			if len(root.fields) != len(value.jsonb.root.fields) || len(root.items) != len(value.jsonb.root.items) {
				t.Fatal("accounting changed structural shape")
			}
			if len(root.fields) > 0 && &root.fields[0] != &value.jsonb.root.fields[0] || len(root.items) > 0 && &root.items[0] != &value.jsonb.root.items[0] {
				t.Fatal("accounting replaced structural backing")
			}
			if cmp, err := compareJSONB(context.Background(), &value.jsonb.root, &fresh.jsonb.root, true); err != nil || cmp != 0 {
				t.Fatal("accounting mutated canonical payload", cmp, err)
			}
		})
	}
}
