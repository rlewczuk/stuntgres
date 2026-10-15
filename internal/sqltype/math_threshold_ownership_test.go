package sqltype

import (
	"context"
	"reflect"
	"testing"
)

func TestMathLogPowerThresholdOwnership(t *testing.T) {
	ctx := context.Background()
	makePlan := func(types map[ID]*Descriptor) (MathPlan, []Value) {
		domain, err := Domain(42000, "bucket_leaf", "public.bucket_leaf", types[Text].Usage())
		if err != nil {
			t.Fatal(err)
		}
		array, err := Array(42001, "_bucket_leaf", domain)
		if err != nil {
			t.Fatal(err)
		}
		p, err := BindWidthBucketThreshold(types[Int4].Usage(), domain.Usage(), array.Usage(), domain.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return p, []Value{arraySearchUnitInput(t, domain, "m"), arraySearchUnitInput(t, array, "[-2:1]={a,m,m,z}")}
	}
	source, target := testTypes(), testTypes()
	old, oldValues := makePlan(source)
	next, nextValues := makePlan(target)
	if old.Equal(next) {
		t.Fatal("foreign same-OID domain authority treated equal")
	}
	rebound, err := BindWidthBucketThreshold(next.result, next.args[0], next.args[1], next.comparison.inputCollation, false)
	if err != nil || !rebound.Equal(next) {
		t.Fatal("rebound comparator authority", err)
	}
	original := append([]Value(nil), oldValues...)
	for _, tc := range []struct {
		p      MathPlan
		values []Value
	}{{old, oldValues}, {rebound, nextValues}} {
		got, err := tc.p.Evaluate(ctx, tc.values, InputEnvironment{})
		if err != nil || got.integer != 3 || got.typ != tc.p.result.Type {
			t.Fatal("domain collated upper bound", got, err)
		}
	}
	if !reflect.DeepEqual(oldValues, original) || old.args[0].Type == next.args[0].Type {
		t.Fatal("source authority mutated")
	}
	for _, values := range [][]Value{{oldValues[0], nextValues[1]}, {nextValues[0], oldValues[1]}, {Null(next.args[0].Type), oldValues[1]}} {
		if got, err := old.Evaluate(ctx, values, InputEnvironment{}); err == nil || got != (Value{}) {
			t.Fatal("mixed generations accepted", got, err)
		}
	}
	conflict, err := BindWidthBucketThreshold(old.result, old.args[0], old.args[1], old.comparison.inputCollation, true)
	if err != nil || conflict.Equal(old) {
		t.Fatal("conflict authority", err)
	}
	got, err := conflict.Evaluate(ctx, oldValues, InputEnvironment{})
	if got != (Value{}) {
		t.Fatal("collation error published datum", got)
	}
	if d, ok := err.(interface{ SQLState() string }); !ok || d.SQLState() != "42P22" {
		t.Fatal(err)
	}
	for _, nullAt := range []int{0, 1} {
		values := append([]Value(nil), oldValues...)
		values[nullAt] = Null(values[nullAt].typ)
		got, err := conflict.Evaluate(ctx, values, InputEnvironment{})
		if err != nil || got != Null(old.result.Type) {
			t.Fatal("strict collation demand", got, err)
		}
	}
	json := source[JSON]
	array := source[199]
	p, err := BindWidthBucketThreshold(source[Int4].Usage(), json.Usage(), array.Usage(), CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ text, code, message string }{
		{"{}", "42883", "could not identify a comparison function for type json"},
		{"{NULL}", "22004", "thresholds array must not contain NULLs"},
		{"{{NULL}}", "2202E", "thresholds must be one-dimensional array"},
	} {
		got, err := p.Evaluate(ctx, []Value{arraySearchUnitInput(t, json, "{}"), arraySearchUnitInput(t, array, tc.text)}, InputEnvironment{})
		arraySearchUnitState(t, err, tc.code, tc.message)
		if got != (Value{}) {
			t.Fatal("error publication", got)
		}
	}
}
