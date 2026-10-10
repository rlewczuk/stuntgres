package sqltype

import (
	"context"
	"errors"
	"testing"
)

func overlapsUnitValue(t *testing.T, d *Descriptor, n int64) Value {
	t.Helper()
	var v Value
	var err error
	if d.ID() == Time || d.ID() == Timetz {
		v, err = TimeValue(d, n)
	} else {
		v, err = TimestampValue(d, n)
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDatetimeOverlapsThirteenPlans(t *testing.T) {
	d := datetimeMakeDescriptors()
	ctx := context.Background()
	for _, id := range []ID{Time, Timestamp, Timestamptz, Timetz} {
		count := 4
		if id == Timetz {
			count = 1
		}
		for mask := range count {
			duration := [2]bool{mask&1 != 0, mask&2 != 0}
			p, err := BindDatetimeOverlaps(d[id], d[Interval], d[Bool], d[Time], duration, [4]bool{})
			if err != nil {
				t.Fatal(id, mask, err)
			}
			values := [4]Value{overlapsUnitValue(t, d[id], 1), overlapsUnitValue(t, d[id], 3), overlapsUnitValue(t, d[id], 2), overlapsUnitValue(t, d[id], 4)}
			for pair, on := range duration {
				if on {
					values[pair*2+1], err = IntervalValue(d[Interval], 0, 0, 2)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			got, slot, err := p.Evaluate(ctx, values, [2]Value{}, InputEnvironment{})
			truth, failure := got.Boolean()
			if err != nil || failure != nil || !truth || slot != -1 || got.Type() != d[Bool] || p.SessionDependent() != (id == Timestamptz && mask != 0) {
				t.Fatal(id, mask, got, slot, err, failure)
			}
		}
	}
}

func TestDatetimeOverlapsTruthAndNullMasks(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeOverlaps(d[Timestamp], d[Interval], d[Bool], d[Time], [2]bool{}, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		points [4]int64
		want   bool
	}{
		{[4]int64{1, 3, 2, 4}, true}, {[4]int64{3, 1, 4, 2}, true}, {[4]int64{1, 2, 2, 3}, false},
		{[4]int64{2, 2, 1, 3}, true}, {[4]int64{1, 1, 1, 3}, true}, {[4]int64{3, 3, 1, 3}, false},
		{[4]int64{2, 2, 2, 2}, true}, {[4]int64{1, 1, 2, 2}, false},
	} {
		var values [4]Value
		for i, n := range tc.points {
			values[i] = overlapsUnitValue(t, d[Timestamp], n)
		}
		got, _, err := p.Evaluate(context.Background(), values, [2]Value{}, InputEnvironment{})
		truth, failure := got.Boolean()
		if err != nil || failure != nil || truth != tc.want {
			t.Fatal(tc, got, err, failure)
		}
	}
	expected := [4][4]any{{true, true, nil, nil}, {nil, nil, nil, nil}, {true, nil, nil, nil}, {nil, nil, nil, nil}}
	for left := range 4 {
		for right := range 4 {
			values := [4]Value{overlapsUnitValue(t, d[Timestamp], 1), overlapsUnitValue(t, d[Timestamp], 4), overlapsUnitValue(t, d[Timestamp], 2), overlapsUnitValue(t, d[Timestamp], 5)}
			for pair, mask := range [2]int{left, right} {
				if mask&2 != 0 {
					values[pair*2] = Null(d[Timestamp])
				}
				if mask&1 != 0 {
					values[pair*2+1] = Null(d[Timestamp])
				}
			}
			got, _, err := p.Evaluate(context.Background(), values, [2]Value{}, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			want := expected[left][right]
			if want == nil {
				if !got.IsNull() {
					t.Fatal(left, right, got)
				}
			} else {
				truth, err := got.Boolean()
				if err != nil || truth != want.(bool) {
					t.Fatal(left, right, got, err)
				}
			}
		}
	}
}

func TestDatetimeOverlapsLocalClockRetagCancelAndAllocation(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeOverlaps(d[Timestamp], d[Interval], d[Bool], d[Time], [2]bool{true, false}, [4]bool{false, true, false, false})
	if err != nil {
		t.Fatal(err)
	}
	values := [4]Value{overlapsUnitValue(t, d[Timestamp], 0), overlapsUnitValue(t, d[Time], 86400000000), overlapsUnitValue(t, d[Timestamp], 43200000000), overlapsUnitValue(t, d[Timestamp], 43200000000)}
	ctx := context.Background()
	got, _, err := p.Evaluate(ctx, values, [2]Value{}, InputEnvironment{})
	truth, failure := got.Boolean()
	if err != nil || failure != nil || !truth {
		t.Fatal(got, err, failure)
	}
	next := datetimeMakeDescriptors()
	rebound, err := p.Retag(func(old *Descriptor) *Descriptor { return next[old.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rebound.Evaluate(ctx, values, [2]Value{}, InputEnvironment{}); err == nil {
		t.Fatal("old provider authority accepted")
	}
	for i, v := range values {
		values[i], err = v.Retag(rebound.ArgumentType(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _, err = rebound.Evaluate(ctx, values, [2]Value{}, InputEnvironment{})
	truth, failure = got.Boolean()
	if err != nil || failure != nil || !truth || got.Type() != next[Bool] {
		t.Fatal(got, err, failure)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := rebound.Evaluate(canceled, values, [2]Value{}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		if _, _, err := rebound.Evaluate(ctx, values, [2]Value{}, InputEnvironment{}); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("OVERLAPS allocated %g times per successful row", allocations)
	}
}
