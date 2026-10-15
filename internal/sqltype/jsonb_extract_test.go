package sqltype

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestJSONBExtractStructuralWorker(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, tc := range []struct {
		doc, path, raw, text string
		null                 bool
	}{
		{`{"a":{"b":1},"a":{}}`, "{a,b}", "", "", true},
		{`{"x":[0],"a":[10,20,30]}`, "{a,-1}", "30", "30", false},
		{`{"a":{"z":1,"z":2,"aa":1.230e-5}}`, "{a}", `{"z": 2, "aa": 0.00001230}`, `{"z": 2, "aa": 0.00001230}`, false},
		{`{"a":"x\ny"}`, "{a}", `"x\ny"`, "x\ny", false},
		{`7`, "{0}", "", "", true},
		{`{"a":[{"b":{"c":9}}]}`, "{{a,0},{b,c}}", "9", "9", false},
	} {
		doc := jsonbSetUnitInput(t, types[JSONB], tc.doc)
		path := jsonbSetUnitInput(t, types[1009], tc.path)
		original := append([]Value(nil), path.array.elements...)
		// Structural traversal must not depend on reparsing the output cache.
		doc.text = "not JSON"
		for _, text := range []bool{false, true} {
			result, want, op, fn := types[JSONB], tc.raw, "#>", "jsonb_extract_path"
			if text {
				result, want, op, fn = types[Text], tc.text, "#>>", "jsonb_extract_path_text"
			}
			binary, err := BindBinary(op, result.Usage(), types[JSONB].Usage(), types[1009].Usage())
			if err != nil {
				t.Fatal(err)
			}
			function, err := BindJSONExtract(fn, result.Usage(), types[JSONB].Usage(), types[1009].Usage(), true)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				for _, eval := range []func() (Value, error){func() (Value, error) { return binary.Evaluate(ctx, doc, path) }, func() (Value, error) { return function.Evaluate(ctx, []Value{doc, path}) }} {
					got, err := eval()
					if err != nil || got.typ != result || got.null != tc.null || got.text != want {
						t.Fatalf("%s %s got=%v err=%v want=%q null=%t", tc.doc, op, got, err, want, tc.null)
					}
				}
			}
		}
		if !slices.Equal(path.array.elements, original) {
			t.Fatal("mutated path")
		}
	}
	doc := jsonbSetUnitInput(t, types[JSONB], `{"a":[{"b":7}]}`)
	original := doc
	path := []Value{String(types[Text], "a")}
	got, err := extractJSONB(ctx, doc, path, types[JSONB])
	if err != nil || got.text != `[{"b": 7}]` || &got.jsonb.root.items[0] != &doc.jsonb.root.fields[0].value.items[0] || doc != original {
		t.Fatal("lost immutable subtree sharing", got, err)
	}
	whole, err := extractJSONB(ctx, doc, nil, types[JSONB])
	if err != nil || whole != doc {
		t.Fatal("empty path copied document", whole, err)
	}
	for _, result := range []*Descriptor{types[JSONB], types[Text]} {
		v, err := extractJSONB(ctx, jsonbSetUnitInput(t, types[JSONB], "null"), nil, result)
		if err != nil || v.typ != result || v.null != (result.ID() == Text) {
			t.Fatal(v, err)
		}
	}
}

func TestJSONBExtractPlanValidation(t *testing.T) {
	types, foreign, ctx := testTypes(), testTypes(), context.Background()
	for _, tc := range []struct {
		name              string
		result, doc, path Usage
		variadic          bool
	}{
		{"jsonb_extract_path", foreign[JSONB].Usage(), types[JSONB].Usage(), types[1009].Usage(), true},
		{"jsonb_extract_path_text", foreign[Text].Usage(), types[JSONB].Usage(), types[1009].Usage(), true},
		{"jsonb_extract_path", types[JSONB].Usage(), types[JSON].Usage(), types[Text].Usage(), false},
		{"jsonb_extract_path", types[JSONB].Usage(), types[JSONB].Usage(), types[Int4].Usage(), false},
	} {
		if _, err := BindJSONExtract(tc.name, tc.result, tc.doc, tc.path, tc.variadic); err == nil {
			t.Fatal("invalid plan", tc)
		}
	}
	plan, err := BindJSONExtract("jsonb_extract_path", types[JSONB].Usage(), types[JSONB].Usage(), types[1009].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := BindBinary("#>", types[JSONB].Usage(), types[JSONB].Usage(), types[1009].Usage())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindBinary("->", foreign[JSONB].Usage(), types[JSONB].Usage(), types[Int4].Usage()); err == nil {
		t.Fatal("foreign result")
	}
	if _, err := BindBinary("#>>", foreign[Text].Usage(), types[JSONB].Usage(), types[1009].Usage()); err == nil {
		t.Fatal("foreign array-path text result")
	}
	path := jsonbSetUnitInput(t, types[1009], "{a}")
	for _, doc := range []Value{Value{typ: types[JSONB]}, Value{typ: types[JSONB], jsonb: &jsonbPayload{root: jsonbNode{kind: 255}}}} {
		if _, err := plan.Evaluate(ctx, []Value{doc, path}); err == nil {
			t.Fatal("invalid payload accepted")
		}
		if _, err := binary.Evaluate(ctx, doc, path); err == nil {
			t.Fatal("invalid binary payload accepted")
		}
	}
	if _, err := plan.Evaluate(ctx, []Value{Null(foreign[JSONB]), Null(types[1009])}); err == nil {
		t.Fatal("foreign NULL document")
	}
	if _, err := plan.Evaluate(ctx, []Value{Null(types[JSONB]), Null(foreign[1009])}); err == nil {
		t.Fatal("foreign NULL path")
	}
	path.array.elements[0] = Null(foreign[Text])
	if _, err := plan.Evaluate(ctx, []Value{Null(types[JSONB]), path}); err == nil {
		t.Fatal("foreign NULL element before strictness")
	}
	if _, err := binary.Evaluate(ctx, Null(types[JSONB]), path); err == nil {
		t.Fatal("foreign binary NULL element before strictness")
	}
	if _, err := plan.Evaluate(ctx, []Value{jsonbSetUnitInput(t, types[JSONB], "{}"), Value{typ: types[1009]}}); err == nil {
		t.Fatal("missing array payload")
	}
	if _, err := binary.Evaluate(ctx, jsonbSetUnitInput(t, types[JSONB], "{}"), Value{typ: types[1009]}); err == nil {
		t.Fatal("missing binary array payload")
	}
	if _, err := binary.Evaluate(ctx, Null(foreign[JSONB]), Null(types[1009])); err == nil {
		t.Fatal("foreign binary NULL document")
	}
	if _, err := binary.Evaluate(ctx, Null(types[JSONB]), Null(foreign[1009])); err == nil {
		t.Fatal("foreign binary NULL path")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := plan.Evaluate(canceled, []Value{Null(types[JSONB]), Null(types[1009])}); got != (Value{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("strictness hid cancellation", got, err)
	}
}

func TestJSONBExtractIndexAndCancellation(t *testing.T) {
	ctx, types := context.Background(), testTypes()
	for _, s := range []string{"01", "+1", "-0", " \t\r\n\v\f1", "", "1 ", "1x", "1.0", "0x1", "1_0", "2147483648", "-2147483649", "-2147483648"} {
		got, err := jsonbExtractIndex(ctx, s)
		if err != nil || got != int64(jsonPathIndex(s)) {
			t.Fatal(s, got, err)
		}
	}
	for _, s := range []string{strings.Repeat(" ", 8*jsonbWorkChunk) + "1", strings.Repeat("0", 8*jsonbWorkChunk) + "1"} {
		cancel := &groupingCancelContext{Context: ctx, at: 3}
		if _, err := jsonbExtractIndex(cancel, s); !errors.Is(err, context.Canceled) || cancel.calls != 3 {
			t.Fatal("index cancellation", err, cancel.calls)
		}
	}
	key := strings.Repeat("x", 8*jsonbWorkChunk)
	root := jsonbNode{kind: jsonbObject, fields: []jsonbEntry{{key: key, value: jsonbNode{kind: jsonbString, text: "found"}}}}
	cancel := &groupingCancelContext{Context: ctx, at: 3}
	if got, err := jsonbExtractField(cancel, &root, key); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("key cancellation", got, err)
	}
	// Long/deep paths are iterative and materialize only the selected leaf.
	node := jsonbNode{kind: jsonbString, text: "leaf"}
	path := make([]Value, 2048)
	for i := range path {
		node = jsonbNode{kind: jsonbArray, items: []jsonbNode{node}}
		path[i] = String(types[Text], "0")
	}
	doc := Value{typ: types[JSONB], jsonb: &jsonbPayload{root: node}}
	got, err := extractJSONB(ctx, doc, path, types[Text])
	if err != nil || got.text != "leaf" {
		t.Fatal(got, err)
	}
	cancel = &groupingCancelContext{Context: ctx, at: 5}
	if got, err := extractJSONB(cancel, doc, path, types[JSONB]); got != (Value{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("deep cancellation", got, err)
	}
	large := Value{typ: types[JSONB], jsonb: &jsonbPayload{root: jsonbNode{kind: jsonbArray, items: []jsonbNode{{kind: jsonbString, text: strings.Repeat("x", 8*jsonbWorkChunk)}}}}}
	cancel = &groupingCancelContext{Context: ctx, at: 5}
	if got, err := extractJSONB(cancel, large, []Value{String(types[Text], "0")}, types[JSONB]); got != (Value{}) || !errors.Is(err, context.Canceled) {
		t.Fatal("output cancellation", got, err)
	}
	for _, i := range []int64{math.MinInt32, math.MaxInt32} {
		if jsonbExtractItem(&large.jsonb.root, i) != nil {
			t.Fatal("index overflow", i)
		}
	}
}
