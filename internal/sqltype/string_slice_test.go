package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func sliceUnitPlan(t *testing.T, name string, source *Descriptor, arity int, types map[ID]*Descriptor) StringPlan {
	t.Helper()
	args := []Usage{source.Usage(), types[Int4].Usage()}
	if arity == 3 {
		args = append(args, types[Int4].Usage())
	}
	plan, err := BindString(name, types[Text].Usage(), types[Text].Usage(), args, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func sliceUnitValues(t *testing.T, types map[ID]*Descriptor, source string, indexes ...int64) [4]Value {
	t.Helper()
	values := [4]Value{String(types[Text], source)}
	for i, n := range indexes {
		value, err := Integer(types[Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		values[i+1] = value
	}
	return values
}

func TestStringSliceIntervals(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		name, source, want string
		indexes            []int64
	}{
		{"left", "abcde", "ab", []int64{2}}, {"left", "abcde", "abc", []int64{-2}},
		{"right", "abcde", "de", []int64{2}}, {"right", "abcde", "cde", []int64{-2}},
		{"left", "abcde", "", []int64{0}}, {"right", "abcde", "", []int64{0}},
		{"left", "abcde", "", []int64{-99}}, {"right", "abcde", "", []int64{-99}},
		{"left", "abcde", "abcde", []int64{math.MaxInt32}}, {"right", "abcde", "abcde", []int64{math.MaxInt32}},
		{"left", "abcde", "", []int64{math.MinInt32}}, {"right", "abcde", "abcde", []int64{math.MinInt32}},
		{"left", "", "", []int64{math.MinInt32}}, {"right", "", "", []int64{math.MinInt32}},
		{"substring", "abcde", "ab", []int64{0, 3}}, {"substring", "abcde", "a", []int64{-2, 4}},
		{"substring", "abcde", "", []int64{-2, 2}}, {"substring", "abcde", "", []int64{1, 0}},
		{"substr", "abcde", "", []int64{99, 2}}, {"substr", "abcde", "bcde", []int64{2, math.MaxInt32}},
		{"substring", "abcde", "", []int64{math.MinInt32, math.MaxInt32}}, {"substring", "abcde", "", []int64{math.MaxInt32, 2}},
		{"substring", "abcde", "abcde", []int64{0}}, {"substr", "abcde", "abcde", []int64{math.MinInt32}},
		{"substr", "abcde", "bcde", []int64{2}}, {"substr", "abcde", "", []int64{99}},
	} {
		plan := sliceUnitPlan(t, tc.name, types[Text], len(tc.indexes)+1, types)
		got, err := plan.Evaluate(context.Background(), sliceUnitValues(t, types, tc.source, tc.indexes...), InputEnvironment{}, OutputEnvironment{})
		text, _ := got.Text()
		if err != nil || text != tc.want || got.Type() != types[Text] {
			t.Fatalf("%s(%q,%v)=%q,%v; want %q", tc.name, tc.source, tc.indexes, text, err, tc.want)
		}
	}
	for _, source := range []string{"", "abc"} {
		for _, start := range []int64{math.MinInt32, 1, 99} {
			plan := sliceUnitPlan(t, "substring", types[Text], 3, types)
			_, err := plan.Evaluate(context.Background(), sliceUnitValues(t, types, source, start, -1), InputEnvironment{}, OutputEnvironment{})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22011", Message: "negative substring length not allowed"}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("negative count", source, start, err)
			}
		}
	}
}

func TestStringSliceUnicodeAndOwners(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		name, source, want string
		indexes            []int64
	}{
		{"left", "Aé🙂Z", "Aé", []int64{2}}, {"right", "Aé🙂Z", "🙂Z", []int64{2}},
		{"left", "e\u0301", "e", []int64{1}}, {"substring", "Aé🙂Z", "é🙂", []int64{2, 2}},
		// These endpoints are above codepoint length but below byte length.
		{"substring", "é🙂", "", []int64{3}}, {"substring", "é🙂", "", []int64{3, 2}},
		{"substring", "é🙂", "🙂", []int64{2, 4}}, {"substring", "é🙂", "🙂", []int64{2, math.MaxInt32}},
		{"substr", "é🙂", "é🙂", []int64{0}}, {"right", "Aé🙂Z", "Aé🙂Z", []int64{math.MinInt32}},
	} {
		plan := sliceUnitPlan(t, tc.name, types[Text], len(tc.indexes)+1, types)
		got, err := plan.Evaluate(context.Background(), sliceUnitValues(t, types, tc.source, tc.indexes...), InputEnvironment{}, OutputEnvironment{})
		text, _ := got.Text()
		if err != nil || text != tc.want {
			t.Fatalf("%s(%q,%v)=%q,%v want %q", tc.name, tc.source, tc.indexes, text, err, tc.want)
		}
	}
	for _, tc := range []struct {
		id   ID
		want string
	}{{BPChar, "ab"}, {VarChar, "  "}} {
		plan := sliceUnitPlan(t, "right", types[tc.id], 2, types)
		values := sliceUnitValues(t, types, "", 2)
		values[0] = String(types[tc.id], "ab  ")
		got, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{})
		text, _ := got.Text()
		original, _ := values[0].Text()
		if err != nil || text != tc.want || original != "ab  " || plan.Result() != types[Text].Usage() || plan.ArgumentUsage(0).Type != types[Text] {
			t.Fatal(tc, text, original, plan, err)
		}
	}
	foreign := mathTypes()
	if _, err := BindString("left", types[Text].Usage(), types[Text].Usage(), []Usage{foreign[Text].Usage(), types[Int4].Usage()}, [4]bool{true}, false); err == nil {
		t.Fatal("foreign source descriptor accepted")
	}
	if _, err := BindString("left", foreign[Text].Usage(), types[Text].Usage(), []Usage{types[Text].Usage(), types[Int4].Usage()}, [4]bool{true}, false); err == nil {
		t.Fatal("foreign result descriptor accepted")
	}
}

func TestStringSliceStrictnessAndOwners(t *testing.T) {
	types := mathTypes()
	foreign := mathTypes()
	for _, name := range []string{"left", "right", "substring", "substr"} {
		arity := 2
		if name == "substring" || name == "substr" {
			arity = 3
		}
		plan := sliceUnitPlan(t, name, types[Text], arity, types)
		values := sliceUnitValues(t, types, "abc", 1, -1)
		for slot := range arity {
			null := values
			null[slot] = Null(null[slot].Type())
			got, err := plan.Evaluate(context.Background(), null, InputEnvironment{}, OutputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Text] {
				t.Fatal(name, slot, got, err)
			}
		}
		for slot := range arity {
			bad := values
			if slot == 0 {
				bad[slot] = String(foreign[Text], "abc")
			} else {
				bad[slot], _ = Integer(foreign[Int4], 1)
			}
			if _, err := plan.Evaluate(context.Background(), bad, InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign input accepted", name, slot)
			}
		}
	}
}

func TestStringSliceCancellationAndAllocations(t *testing.T) {
	types := mathTypes()
	long := strings.Repeat("é🙂", 32768)
	for _, tc := range []struct {
		name    string
		indexes []int64
	}{
		{"left", []int64{40000}}, {"left", []int64{-1}}, {"right", []int64{2}},
		{"right", []int64{-40000}}, {"substring", []int64{40000, 2}}, {"substr", []int64{1, 40000}},
	} {
		plan := sliceUnitPlan(t, tc.name, types[Text], len(tc.indexes)+1, types)
		values := sliceUnitValues(t, types, long, tc.indexes...)
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 8}
		if _, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{}); err != context.Canceled {
			t.Fatal("in-flight count/cut failed to cancel", tc, err)
		}
		if _, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{}); err != nil {
			t.Fatal("canceled plan retained state", tc, err)
		}
		allocations := testing.AllocsPerRun(10, func() {
			if _, err := plan.Evaluate(context.Background(), values, InputEnvironment{}, OutputEnvironment{}); err != nil {
				panic(err)
			}
		})
		if allocations != 0 {
			t.Fatal("text view allocated", tc, allocations)
		}
	}
}

func TestStringSliceBoundedSuffixWork(t *testing.T) {
	types := mathTypes()
	source := strings.Repeat("é🙂", 32768)
	for _, tc := range []struct {
		name        string
		indexes     []int64
		want        string
		checkpoints int
	}{
		{"right", []int64{-1}, source[2:], 64},
		{"substring", []int64{2}, source[2:], 64},
		{"substr", []int64{1}, source, 64},
		{"substr", []int64{0}, source, 64},
		{"substring", []int64{2, math.MaxInt32}, source[2:], 64},
		{"substring", []int64{1, int64(len(source))}, source, 64},
		{"left", []int64{math.MaxInt32}, source, 64},
		{"right", []int64{math.MaxInt32}, source, 64},
		{"right", []int64{math.MinInt32}, source, 64},
		// Positive right needs one count pass, then only a one-character cut.
		// Allow that pass with slack, but not another scan of the long suffix.
		{"right", []int64{65535}, source[2:], len(source)/256 + 64},
	} {
		t.Run(tc.name+fmt.Sprint(tc.indexes), func(t *testing.T) {
			plan := sliceUnitPlan(t, tc.name, types[Text], len(tc.indexes)+1, types)
			values := sliceUnitValues(t, types, source, tc.indexes...)
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: tc.checkpoints}
			got, err := plan.Evaluate(ctx, values, InputEnvironment{}, OutputEnvironment{})
			text, _ := got.Text()
			if err != nil || text != tc.want {
				t.Fatalf("bounded suffix slice returned %q, %v; want unchanged suffix of %d bytes", text, err, len(tc.want))
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := plan.Evaluate(canceled, values, InputEnvironment{}, OutputEnvironment{}); err != context.Canceled {
				t.Fatal("known suffix bypassed provider-entry cancellation", err)
			}
		})
	}
}
