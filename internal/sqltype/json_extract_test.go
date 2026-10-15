package sqltype

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
)

func TestJSONExtractTypedWorker(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		doc, path, raw, text string
		nullText             bool
	}{
		{`{"a":{"b":1},"a":{}}`, "{a,b}", "1", "1", false},
		{`{"x":[0],"a":[10,20,30]}`, "{a,-1}", "10", "10", false},
		{`{"a":{ "z":1, "z":2 }}`, "{a}", `{ "z":1, "z":2 }`, `{ "z":1, "z":2 }`, false},
		{`{"a":"x\ny"}`, "{a}", `"x\ny"`, "x\ny", false},
		{`{"a":null}`, "{a}", "null", "", true},
		{"  1e+09  ", "{}", "  1e+09", "  1e+09", false},
	} {
		doc := jsonbSetUnitInput(t, types[JSON], tc.doc)
		path := jsonbSetUnitInput(t, types[1009], tc.path)
		original := append([]Value(nil), path.array.elements...)
		for _, op := range []string{"#>", "#>>"} {
			result := types[JSON]
			want := tc.raw
			isnull := false
			if op == "#>>" {
				result, want, isnull = types[Text], tc.text, tc.nullText
			}
			plan, err := BindBinary(op, result.Usage(), types[JSON].Usage(), types[1009].Usage())
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				got, err := plan.Evaluate(ctx, doc, path)
				if err != nil || got.typ != result || got.null != isnull || got.text != want {
					t.Fatalf("%s %s %s got=%v err=%v want=%q null=%t", tc.doc, op, tc.path, got, err, want, isnull)
				}
			}
			if !slices.Equal(path.array.elements, original) {
				t.Fatal("path mutation")
			}
		}
	}
}
func TestJSONExtractIndexParsing(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want int
	}{{"01", 1}, {"+1", 1}, {"-0", 0}, {" \t\r\n1", 1}, {"", math.MinInt32}, {"1 ", math.MinInt32}, {"1x", math.MinInt32}, {"1.0", math.MinInt32}, {"0x1", math.MinInt32}, {"1_0", math.MinInt32}, {"2147483648", math.MinInt32}, {"-2147483649", math.MinInt32}, {"-2147483648", math.MinInt32}} {
		if got := jsonPathIndex(tc.s); got != tc.want {
			t.Fatalf("%q=%d want%d", tc.s, got, tc.want)
		}
	}
}
func TestJSONExtractPlanValidationAndCancellation(t *testing.T) {
	types := testTypes()
	foreign := testTypes()
	good, err := BindJSONExtract("json_extract_path", types[JSON].Usage(), types[JSON].Usage(), types[1009].Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		result, doc, path Usage
		variadic          bool
	}{
		{"json_extract_path", foreign[JSON].Usage(), types[JSON].Usage(), types[1009].Usage(), true},
		{"json_extract_path_text", foreign[Text].Usage(), types[JSON].Usage(), types[1009].Usage(), true},
		{"json_extract_path", types[JSON].Usage(), types[JSONB].Usage(), types[1009].Usage(), true},
		{"json_extract_path", types[JSON].Usage(), types[JSON].Usage(), types[Text].Usage(), true},
		{"json_extract_path", types[JSON].Usage(), types[JSON].Usage(), types[Int4].Usage(), false},
		{"other", types[JSON].Usage(), types[JSON].Usage(), types[Text].Usage(), false},
	} {
		if _, err := BindJSONExtract(tc.name, tc.result, tc.doc, tc.path, tc.variadic); err == nil {
			t.Fatal("accepted invalid plan", tc)
		}
	}
	values := []Value{jsonbSetUnitInput(t, types[JSON], `{"a":1}`), jsonbSetUnitInput(t, types[1009], "{a}")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := good.Evaluate(ctx, values); !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal(got, err)
	}
	values[0] = jsonbSetUnitInput(t, types[JSON], `{"x":"\u0000"}`)
	values[1] = jsonbSetUnitInput(t, types[1009], "{missing,NULL}")
	got, err := good.Evaluate(context.Background(), values)
	if err != nil || !got.null || got.typ != types[JSON] {
		t.Fatal(got, err)
	}
	values[1] = Value{typ: types[1009]}
	if _, err := good.Evaluate(context.Background(), values); err == nil {
		t.Fatal("missing array payload accepted")
	}
	if _, err := good.Evaluate(context.Background(), nil); err == nil {
		t.Fatal("missing arguments accepted")
	}
}
