package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDatetimeJustifyComponents(t *testing.T) {
	d := datetimeMakeDescriptors()[Interval]
	type parts struct {
		m, d int32
		t    int64
	}
	for _, tc := range []struct {
		name     string
		in, want parts
	}{
		{"justify_days", parts{0, 29, 0}, parts{0, 29, 0}}, {"justify_days", parts{0, 30, 0}, parts{1, 0, 0}}, {"justify_days", parts{0, 31, 0}, parts{1, 1, 0}},
		{"justify_days", parts{0, -29, 0}, parts{0, -29, 0}}, {"justify_days", parts{0, -30, 0}, parts{-1, 0, 0}}, {"justify_days", parts{0, -31, 0}, parts{-1, -1, 0}},
		{"justify_days", parts{1, -1, math.MaxInt64}, parts{0, 29, math.MaxInt64}}, {"justify_days", parts{-1, 1, math.MinInt64}, parts{0, -29, math.MinInt64}},
		{"justify_hours", parts{6, 0, microsPerDay - 1}, parts{6, 0, microsPerDay - 1}}, {"justify_hours", parts{6, 0, microsPerDay}, parts{6, 1, 0}}, {"justify_hours", parts{6, 0, microsPerDay + 1}, parts{6, 1, 1}},
		{"justify_hours", parts{-6, 0, -microsPerDay - 1}, parts{-6, -1, -1}}, {"justify_hours", parts{6, 1, -3600000000}, parts{6, 0, 82800000000}}, {"justify_hours", parts{-6, -1, 3600000000}, parts{-6, 0, -82800000000}},
		{"justify_interval", parts{1, 0, -3600000000}, parts{0, 29, 82800000000}}, {"justify_interval", parts{-1, 0, 3600000000}, parts{0, -29, -82800000000}},
		{"justify_interval", parts{1, -1, -3600000000}, parts{0, 28, 82800000000}}, {"justify_interval", parts{-1, 1, 3600000000}, parts{0, -28, -82800000000}},
		{"justify_interval", parts{1, -30, 1}, parts{0, 0, 1}}, {"justify_interval", parts{1, 1, -90000000000}, parts{0, 29, 82800000000}}, {"justify_interval", parts{1, -30, 0}, parts{0, 0, 0}},
		{"justify_interval", parts{0, math.MaxInt32, microsPerDay}, parts{71582788, 8, 0}}, {"justify_interval", parts{0, math.MinInt32, -microsPerDay}, parts{-71582788, -9, 0}},
		{"justify_interval", parts{math.MaxInt32, 30, -microsPerDay}, parts{math.MaxInt32, 29, 0}}, {"justify_interval", parts{math.MinInt32, -30, microsPerDay}, parts{math.MinInt32, -29, 0}},
		{"justify_hours", parts{0, 0, math.MaxInt64}, parts{0, 106751991, 14454775807}}, {"justify_hours", parts{0, 0, math.MinInt64}, parts{0, -106751991, -14454775808}},
		{"justify_interval", parts{0, 0, math.MaxInt64}, parts{3558399, 21, 14454775807}}, {"justify_interval", parts{0, 0, math.MinInt64}, parts{-3558399, -21, -14454775808}},
		{"justify_days", parts{0, 0, math.MaxInt64}, parts{0, 0, math.MaxInt64}}, {"justify_days", parts{0, 0, math.MinInt64}, parts{0, 0, math.MinInt64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := BindDatetimeJustify(tc.name, d, d)
			if err != nil {
				t.Fatal(err)
			}
			input, err := IntervalValue(d, tc.in.m, tc.in.d, tc.in.t)
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Evaluate(context.Background(), input)
			if err != nil || v.Type() != d || (parts{v.months, v.days, v.integer}) != tc.want {
				t.Fatal(tc, v, err)
			}
			if (parts{input.months, input.days, input.integer}) != tc.in {
				t.Fatal("input mutated")
			}
			if tc.name == "justify_interval" {
				again, err := p.Evaluate(context.Background(), v)
				if err != nil || again != v {
					t.Fatal("non-idempotent", again, err)
				}
			}
		})
	}
}

func TestDatetimeJustifyOverflow(t *testing.T) {
	d := datetimeMakeDescriptors()[Interval]
	for _, tc := range []struct {
		name  string
		m, d  int32
		clock int64
	}{
		{"justify_days", math.MaxInt32, 30, 0}, {"justify_days", math.MinInt32, -30, 0},
		{"justify_hours", 0, math.MaxInt32, microsPerDay}, {"justify_hours", 0, math.MinInt32, -microsPerDay},
		{"justify_interval", math.MaxInt32, 30, 0}, {"justify_interval", math.MinInt32, -30, 0},
		{"justify_interval", math.MaxInt32, 30, 1}, {"justify_interval", math.MinInt32, -30, -1},
		{"justify_interval", math.MaxInt32, -30, 60 * microsPerDay}, {"justify_interval", math.MinInt32, 30, -60 * microsPerDay},
	} {
		p, err := BindDatetimeJustify(tc.name, d, d)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := IntervalValue(d, tc.m, tc.d, tc.clock)
		_, err = p.Evaluate(context.Background(), v)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22008" || diagnostic.Message != "interval out of range" {
			t.Fatal(tc, err)
		}
	}
}

func TestDatetimeJustifyOwnershipNullInfinityContext(t *testing.T) {
	d, other := datetimeMakeDescriptors()[Interval], datetimeMakeDescriptors()[Interval]
	for _, name := range []string{"justify_days", "justify_hours", "justify_interval"} {
		p, err := BindDatetimeJustify(name, d, d)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(context.Background(), Null(d))
		if err != nil || !v.IsNull() || v.Type() != d {
			t.Fatal(v, err)
		}
		for _, in := range []Value{{typ: d, months: math.MaxInt32, days: math.MaxInt32, integer: math.MaxInt64}, {typ: d, months: math.MinInt32, days: math.MinInt32, integer: math.MinInt64}} {
			v, err := p.Evaluate(context.Background(), in)
			if err != nil || v != in {
				t.Fatal(v, err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, Null(d)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(context.Background(), Null(other)); err == nil {
			t.Fatal("foreign descriptor accepted")
		}
		next, err := p.Retag(func(*Descriptor) *Descriptor { return other })
		if err != nil {
			t.Fatal(err)
		}
		v, err = next.Evaluate(context.Background(), Null(other))
		if err != nil || v.Type() != other {
			t.Fatal(v, err)
		}
		if _, err := next.Evaluate(context.Background(), Null(d)); err == nil {
			t.Fatal("old owner accepted")
		}
	}
	if _, err := BindDatetimeJustify("age", d, d); err == nil {
		t.Fatal("unselected function accepted")
	}
	if _, err := BindDatetimeJustify("justify_days", nil, d); err == nil {
		t.Fatal("nil descriptor accepted")
	}
}
