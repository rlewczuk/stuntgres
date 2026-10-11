package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func splitUnitPlan(t *testing.T, name string, types map[ID]*Descriptor, values [4]Value, count int) StringPlan {
	t.Helper()
	args := make([]Usage, count)
	flags := [4]bool{true, true, true}
	for i := range args {
		args[i] = values[i].Type().Usage()
	}
	result := types[Text].Usage()
	if name == "string_to_array" {
		result = types[types[Text].AssociatedArray()].Usage()
	}
	if name == "split_part" {
		flags[2] = false
	}
	plan, err := BindString(name, result, types[Text].Usage(), args, flags, false)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func TestStringSplitPart(t *testing.T) {
	for _, tc := range []struct {
		source, delimiter string
		index             int64
		want              string
	}{
		{"a,b,c", ",", 2, "b"}, {"a,b,c", ",", -2, "b"}, {"a,b", ",", math.MinInt32, ""}, {"abc", "", -1, "abc"}, {"abc", "", 2, ""}, {"", "", 1, ""}, {"aaa", "aa", 2, "a"}, {"é🙂é", "é", 2, "🙂"}, {",x,,x,", ",", -1, ""},
	} {
		got, err := splitPart(context.Background(), tc.source, tc.delimiter, tc.index)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := splitPart(context.Background(), "", "", 0); err == nil {
		t.Fatal("zero accepted")
	}
	types := testTypes()
	values := [4]Value{Null(types[Text]), String(types[Text], ","), {}}
	values[2], _ = Integer(types[Int4], 0)
	plan := splitUnitPlan(t, "split_part", types, values, 3)
	got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
	if err != nil || !got.IsNull() || got.Type() != types[Text] || !plan.Strict() {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := splitPart(ctx, "a,b", ",", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestStringSplitArray(t *testing.T) {
	types := testTypes()
	text := types[Text]
	array := types[text.AssociatedArray()]
	for _, tc := range []struct {
		source, delimiter, marker Value
		want                      string
		rank, length              int
	}{
		{String(text, "x,x,,"), String(text, ","), String(text, ""), `{x,x,NULL,NULL}`, 1, 4},
		{String(text, ""), Null(text), String(text, ""), `{}`, 0, 0},
		{String(text, "é🙂é"), Null(text), Null(text), `{é,🙂,e,́}`, 1, 4},
		{String(text, "NULL,x,NULL"), String(text, ","), Null(text), `{"NULL",x,"NULL"}`, 1, 3},
	} {
		values := [4]Value{tc.source, tc.delimiter, tc.marker}
		plan := splitUnitPlan(t, "string_to_array", types, values, 3)
		got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != array || got.ArrayLen() != tc.length || got.ArrayRank() != tc.rank || plan.Strict() {
			t.Fatal(got, err)
		}
		if got.Type().Element() != text {
			t.Fatal("element owner")
		}
		encoded, err := array.Output(context.Background(), got)
		if err != nil || encoded != tc.want {
			t.Fatal(encoded, err)
		}
		dims := got.ArrayDimensions()
		if len(dims) != tc.rank || len(dims) > 0 && (dims[0].Lower != 1 || int(dims[0].Length) != tc.length) {
			t.Fatal(dims)
		}
	}
	values := [4]Value{Null(text), Null(text), Null(text)}
	plan := splitUnitPlan(t, "string_to_array", types, values, 3)
	got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
	if err != nil || !got.IsNull() || got.Type() != array {
		t.Fatal(got, err)
	}
	fresh := testTypes()
	values[0] = String(fresh[Text], "a")
	if _, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("foreign input owner accepted")
	}
}
func TestStringSplitTable(t *testing.T) {
	types := testTypes()
	text := types[Text]
	values := [4]Value{String(text, "x,,"), String(text, ","), String(text, "")}
	plan := splitUnitPlan(t, "string_to_table", types, values, 3)
	cursor, err := plan.OpenStringTable(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		v, present, err := cursor.Next(context.Background())
		if err != nil || !present || v.Type() != text || v.IsNull() != (i > 0) {
			t.Fatal(i, v, present, err)
		}
	}
	if _, present, err := cursor.Next(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	cursor.Close()
	cursor.Close()
	values[0] = Null(text)
	cursor, err = plan.OpenStringTable(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := cursor.Next(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cursor.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStringSplitTableCancellationWork(t *testing.T) {
	types := testTypes()
	text := types[Text]
	values := [4]Value{String(text, strings.Repeat("ab", 1<<18)), String(text, "az")}
	plan := splitUnitPlan(t, "string_to_table", types, values, 2)
	cursor, err := plan.OpenStringTable(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counted := &stringTrimCancellationContext{Context: ctx, cancel: cancel, cancelAt: 6}
	if value, present, err := cursor.Next(counted); !errors.Is(err, context.Canceled) || present || value.Type() != nil || counted.calls < 6 {
		t.Fatal(value, present, counted.calls, err)
	}
	cursor.Close()
	if _, present, err := cursor.Next(context.Background()); err != nil || present {
		t.Fatal(present, err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	arrayContext := &stringTrimCancellationContext{Context: ctx2, cancel: cancel2, cancelAt: 6}
	arrayPlan := splitUnitPlan(t, "string_to_array", types, values, 2)
	if value, err := arrayPlan.Evaluate(arrayContext, values, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) || value.Type() != nil {
		t.Fatal(value, err)
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	countContext := &stringTrimCancellationContext{Context: ctx3, cancel: cancel3, cancelAt: 6}
	if _, err := splitPart(countContext, strings.Repeat("x,", 1<<18), ",", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
