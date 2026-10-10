package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestDatetimeAgeComponents(t *testing.T) {
	d := datetimeMakeDescriptors()
	for _, tc := range []struct {
		left, right  string
		months, days int32
		micros       int64
	}{
		{"2001-04-10", "1957-06-13", 525, 27, 0},
		{"2004-06-01", "2004-04-30", 1, 1, 0},
		{"2023-02-28", "2023-01-31", 0, 28, 0},
		{"2024-02-29", "2024-01-31", 0, 29, 0},
		{"2024-03-31", "2024-02-29", 1, 2, 0},
		{"2004-06-01 01:00", "2004-04-30 02:00", 1, 0, 82800000000},
		{"2000-03-01 00:00:00.000001", "2000-02-29 23:59:59.999999", 0, 0, 2},
		{"1999-12-31 00:00:00.000001", "1999-12-30 23:59:59.999999", 0, 0, 2},
		{"0001-01-01 AD", "0001-01-01 BC", 12, 0, 0},
		{"0001-01-01 AD", "0001-12-31 BC", 0, 1, 0},
		{"0001-03-01 BC", "0001-02-28 BC", 0, 2, 0},
		{"294276-12-31 23:59:59.999999", "4714-11-24 BC", 3587869, 7, 86399999999},
		{"2000-01-01", "2000-01-01", 0, 0, 0},
	} {
		t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
			left, err := d[Timestamp].Input(context.Background(), tc.left, -1)
			if err != nil {
				t.Fatal(err)
			}
			right, err := d[Timestamp].Input(context.Background(), tc.right, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				l, r, m, day, clock := left.integer, right.integer, tc.months, tc.days, tc.micros
				if reverse {
					l, r, m, day, clock = r, l, -m, -day, -clock
				}
				v, err := symbolicTimestampAge(d[Interval], l, r)
				if err != nil || v.months != m || v.days != day || v.integer != clock || v.Type() != d[Interval] {
					t.Fatal(v, err, m, day, clock)
				}
			}
		})
	}
}

func TestDatetimeAgeFiniteInfinityAndNull(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeAgeFinite("age", d[Timestamp], d[Interval], d[Date], 2)
	if err != nil {
		t.Fatal(err)
	}
	values := []int64{0, TimestampNegativeInfinity, TimestampPositiveInfinity}
	for _, l := range values {
		for _, r := range values {
			left, _ := TimestampValue(d[Timestamp], l)
			right, _ := TimestampValue(d[Timestamp], r)
			v, err := p.Evaluate(context.Background(), []Value{left, right}, InputEnvironment{})
			if l == r && l != 0 {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22008" || diagnostic.Message != "interval out of range" {
					t.Fatal(err)
				}
			} else {
				want := timestampInfinity(l)
				if want == 0 {
					want = -timestampInfinity(r)
				}
				if err != nil || intervalInfinity(v) != want {
					t.Fatal(l, r, v, err)
				}
			}
			v, err = p.Evaluate(context.Background(), []Value{Null(d[Timestamp]), right}, InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != d[Interval] {
				t.Fatal(v, err)
			}
		}
	}
	for _, id := range []ID{Date, Timestamp, Interval} {
		p, err := BindDatetimeAgeFinite("isfinite", d[id], d[Bool], d[Date], 1)
		if err != nil {
			t.Fatal(err)
		}
		finite := []Value{}
		switch id {
		case Date:
			for _, n := range []int64{dateMinimum, 0, dateMaximum} {
				v, _ := DateValue(d[id], n)
				finite = append(finite, v)
			}
		case Timestamp:
			for _, n := range []int64{timestampMinimum, 0, timestampEnd - 1} {
				v, _ := TimestampValue(d[id], n)
				finite = append(finite, v)
			}
		case Interval:
			for _, parts := range []struct {
				m, day int32
				clock  int64
			}{{0, 0, 0}, {1, -2, 3}, {math.MaxInt32, 0, 0}, {math.MinInt32, 0, 0}, {0, math.MaxInt32, 0}, {0, math.MinInt32, 0}, {0, 0, math.MaxInt64}, {0, 0, math.MinInt64}} {
				v, err := IntervalValue(d[id], parts.m, parts.day, parts.clock)
				if err != nil {
					t.Fatal(err)
				}
				finite = append(finite, v)
			}
		}
		for _, arg := range finite {
			v, err := p.Evaluate(context.Background(), []Value{arg}, InputEnvironment{})
			if err != nil || !v.boolean {
				t.Fatal(id, arg, v, err)
			}
		}
		for _, sign := range []int{-1, 1} {
			var arg Value
			switch id {
			case Date:
				n := DatePositiveInfinity
				if sign < 0 {
					n = DateNegativeInfinity
				}
				arg, _ = DateValue(d[id], n)
			case Timestamp:
				n := TimestampPositiveInfinity
				if sign < 0 {
					n = TimestampNegativeInfinity
				}
				arg, _ = TimestampValue(d[id], n)
			case Interval:
				arg, _ = arithmeticIntervalInfinity(d[id], sign)
			}
			v, err := p.Evaluate(context.Background(), []Value{arg}, InputEnvironment{})
			if err != nil || v.boolean {
				t.Fatal(id, v, err)
			}
		}
		v, err := p.Evaluate(context.Background(), []Value{Null(d[id])}, InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != d[Bool] {
			t.Fatal(v, err)
		}
	}
}

func TestDatetimeAgeUnaryEnvironmentAndAllocations(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeAgeFinite("age", d[Timestamp], d[Interval], d[Date], 1)
	if err != nil {
		t.Fatal(err)
	}
	arg, _ := TimestampValue(d[Timestamp], 0)
	stamp := time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		zone string
		days int32
	}{{"Pacific/Kiritimati", 1}, {"Pacific/Pago_Pago", 0}} {
		zone, err := ResolveZone(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(context.Background(), []Value{arg}, InputEnvironment{TransactionStart: stamp, Zone: zone})
		if err != nil || v.months != 0 || v.days != tc.days || v.integer != 0 {
			t.Fatal(tc, v, err)
		}
		for _, sign := range []int{-1, 1} {
			n := TimestampPositiveInfinity
			if sign < 0 {
				n = TimestampNegativeInfinity
			}
			infinite, _ := TimestampValue(d[Timestamp], n)
			v, err := p.Evaluate(context.Background(), []Value{infinite}, InputEnvironment{TransactionStart: stamp, Zone: zone})
			if err != nil || intervalInfinity(v) != -sign {
				t.Fatal(v, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, []Value{arg}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	binary, err := BindDatetimeAgeFinite("age", d[Timestamp], d[Interval], d[Date], 2)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := TimestampValue(d[Timestamp], timestampEnd-1)
	right, _ := TimestampValue(d[Timestamp], timestampMinimum)
	args := [2]Value{left, right}
	if n := testing.AllocsPerRun(100, func() {
		v, err := binary.Evaluate(context.Background(), args[:], InputEnvironment{})
		if err != nil || v.months != 3587869 {
			panic("age allocation witness failed")
		}
	}); n != 0 {
		t.Fatal("successful binary age allocated", n)
	}
	finite, err := BindDatetimeAgeFinite("isfinite", d[Timestamp], d[Bool], d[Date], 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(100, func() {
		v, err := finite.Evaluate(context.Background(), args[:1], InputEnvironment{})
		if err != nil || !v.boolean {
			panic("isfinite allocation witness failed")
		}
	}); n != 0 {
		t.Fatal("successful isfinite allocated", n)
	}
}
