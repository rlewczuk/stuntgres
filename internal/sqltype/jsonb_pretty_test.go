package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestJSONBPrettyPayloadOwnership(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	plan, err := BindBinary("jsonb_pretty", types[Text].Usage(), types[JSONB].Usage())
	if err != nil {
		t.Fatal(err)
	}
	value := jsonbSetUnitInput(t, types[JSONB], `{"zz":0,"a":[{},true,"x\n"],"zz":2}`)
	before, err := jsonbOutput(ctx, &value.jsonb.root)
	if err != nil {
		t.Fatal(err)
	}
	value.text = "deliberately invalid cached JSON"
	original := value
	const want = "{\n    \"a\": [\n        {\n        },\n        true,\n        \"x\\n\"\n    ],\n    \"zz\": 2\n}"
	for range 2 {
		got, err := plan.Evaluate(ctx, value)
		if err != nil || got.typ != types[Text] || got.null || got.text != want || got.jsonb != nil {
			t.Fatalf("result=%v error=%v", got, err)
		}
		if value != original {
			t.Fatal("mutated input value")
		}
		after, err := jsonbOutput(ctx, &value.jsonb.root)
		if err != nil || after != before {
			t.Fatal("mutated payload", after, err)
		}
	}
	null, err := plan.Evaluate(ctx, Null(types[JSONB]))
	if err != nil || null != Null(types[Text]) {
		t.Fatal("untyped NULL", null, err)
	}
	for _, bad := range []Value{{typ: types[JSONB]}, {typ: types[JSONB], jsonb: &jsonbPayload{root: jsonbNode{kind: 255}}}} {
		got, err := plan.Evaluate(ctx, bad)
		if err == nil || got != (Value{}) {
			t.Fatal("invalid payload published result", got, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	got, err := plan.Evaluate(canceled, Null(types[JSONB]))
	if !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal("NULL hid cancellation", got, err)
	}
}

func TestJSONBPrettyCancellationCheckpoints(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	plan, err := BindBinary("jsonb_pretty", types[Text].Usage(), types[JSONB].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source string }{
		{"scalar-string", `"` + strings.Repeat(`\n`, 4096) + `"`},
		{"first-key", `{"` + strings.Repeat("a", 4096) + `":1}`},
		{"later-key", `{"a":0,"` + strings.Repeat("z", 4096) + `":1}`},
		{"nested", strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40)},
		{"wide", "[" + strings.Repeat("0,", 128) + "0]"},
		{"numeric-digits", strings.Repeat("1", 4096)},
		{"numeric-integer-zeros", "1e8192"},
		{"numeric-fraction-zeros", "1e-8192"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := jsonbSetUnitInput(t, types[JSONB], tc.source)
			original := value
			baseline, err := jsonbOutput(ctx, &value.jsonb.root)
			if err != nil {
				t.Fatal(err)
			}
			calibration := &groupingCancelContext{Context: ctx, at: int(^uint(0) >> 1)}
			if _, err := plan.Evaluate(calibration, value); err != nil || calibration.calls < 4 {
				t.Fatal("missing checkpoints", calibration.calls, err)
			}
			// Every actual checkpoint, including later traversal/writer phases, must
			// discard the private output and leave the published payload untouched.
			for at := 1; at <= calibration.calls; at++ {
				active := &groupingCancelContext{Context: ctx, at: at}
				got, err := plan.Evaluate(active, value)
				if !errors.Is(err, context.Canceled) || got != (Value{}) {
					t.Fatalf("checkpoint %d published %v: %v", at, got, err)
				}
				if value != original {
					t.Fatal("mutated input")
				}
			}
			after, err := jsonbOutput(ctx, &value.jsonb.root)
			if err != nil || after != baseline {
				t.Fatal("cancellation mutated payload", err)
			}
		})
	}
	var b strings.Builder
	active := &groupingCancelContext{Context: ctx, at: 5}
	if err := jsonbPrettyLine(active, &b, 2048); !errors.Is(err, context.Canceled) || b.Len() >= 1+2048*4 {
		t.Fatal("indentation failed to poll", b.Len(), err)
	}
}

func TestJSONBPrettyNumericAppend(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, tc := range []struct{ input, want string }{
		{"0", "0"}, {"-12.3400", "-12.3400"}, {"1.230e-5", "0.00001230"},
		{"1e4096", "1" + strings.Repeat("0", 4096)},
		{"1e-4096", "0." + strings.Repeat("0", 4095) + "1"},
		{strings.Repeat("9", 4096), strings.Repeat("9", 4096)},
		{"NaN", "NaN"}, {"Infinity", "Infinity"}, {"-Infinity", "-Infinity"},
	} {
		v, err := types[Numeric].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString("prefix:")
		if err := appendNumericOutput(ctx, &b, v); err != nil || b.String() != "prefix:"+tc.want || numericOutput(v) != tc.want {
			t.Fatalf("numeric %q=%q error=%v", tc.input, b.String(), err)
		}
	}
}
