package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func datetimeBinUnitArgs(d map[ID]*Descriptor, id ID, months, days int32, stride, source, origin int64) [3]Value {
	span, _ := IntervalValue(d[Interval], months, days, stride)
	s, _ := TimestampValue(d[id], source)
	o, _ := TimestampValue(d[id], origin)
	return [3]Value{span, s, o}
}

func datetimeBinUnitFailure(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic %#v want %#v", err, want)
	}
}

func TestDatetimeBinPlan(t *testing.T) {
	d, next := datetimeMakeDescriptors(), datetimeMakeDescriptors()
	for _, id := range []ID{Timestamp, Timestamptz} {
		p, err := BindDatetimeBin(d[Interval], d[id], d[id])
		if err != nil {
			t.Fatal(err)
		}
		args := datetimeBinUnitArgs(d, id, 0, 0, 10, 23, 0)
		for i := range args {
			null := args
			null[i] = Null(null[i].Type())
			v, err := p.Evaluate(context.Background(), null[:])
			if err != nil || !v.IsNull() || v.Type() != d[id] {
				t.Fatal(v, err)
			}
		}
		for _, bad := range [][]Value{nil, args[:2], {args[0], Null(next[id]), args[2]}, {Null(next[Interval]), args[1], args[2]}} {
			if _, err := p.Evaluate(context.Background(), bad); err == nil {
				t.Fatal("invalid provider arguments accepted")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, args[:]); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		retagged, err := p.Retag(func(old *Descriptor) *Descriptor { return next[old.ID()] })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := retagged.Evaluate(context.Background(), args[:]); err == nil {
			t.Fatal("old authority accepted")
		}
		for i, arg := range args {
			args[i], err = arg.Retag(next[arg.Type().ID()])
			if err != nil {
				t.Fatal(err)
			}
		}
		v, err := retagged.Evaluate(context.Background(), args[:])
		if err != nil || v.Type() != next[id] || v.integer != 20 {
			t.Fatal(v, err)
		}
	}
	for _, tc := range [][3]*Descriptor{{nil, d[Timestamp], d[Timestamp]}, {d[Text], d[Timestamp], d[Timestamp]}, {d[Interval], d[Date], d[Date]}, {d[Interval], d[Timestamp], d[Timestamptz]}, {d[Interval], nil, d[Timestamp]}} {
		if _, err := BindDatetimeBin(tc[0], tc[1], tc[2]); err == nil {
			t.Fatal("invalid provider plan accepted")
		}
	}
}

func TestDatetimeBinAlignment(t *testing.T) {
	d := datetimeMakeDescriptors()
	for _, id := range []ID{Timestamp, Timestamptz} {
		p, _ := BindDatetimeBin(d[Interval], d[id], d[id])
		for _, tc := range []struct{ span, source, origin, want int64 }{
			{900000000, 149999999, 150000000, -750000000},
			{900000000, 150000000, 150000000, 150000000},
			{900000000, 1049999999, 150000000, 150000000},
			{900000000, 1050000000, 150000000, 1050000000},
			{15, -30, 0, -30}, {15, -31, 0, -45},
			{250, 99, 100, -150}, {250, 100, 100, 100}, {250, 349, 100, 100}, {250, 350, 100, 350},
		} {
			args := datetimeBinUnitArgs(d, id, 0, 0, tc.span, tc.source, tc.origin)
			v, err := p.Evaluate(context.Background(), args[:])
			if err != nil || v.integer != tc.want || v.Type() != d[id] {
				t.Fatal(tc, v, err)
			}
		}
		args := datetimeBinUnitArgs(d, id, 0, 1, -3600000000, microsPerDay, 0)
		v, err := p.Evaluate(context.Background(), args[:])
		if err != nil || v.integer != 82800000000 {
			t.Fatal(v, err)
		}
	}
}

func TestDatetimeBinErrorPrecedence(t *testing.T) {
	d := datetimeMakeDescriptors()
	for _, id := range []ID{Timestamp, Timestamptz} {
		p, _ := BindDatetimeBin(d[Interval], d[id], d[id])
		for _, tc := range []struct {
			months, days  int32
			span, origin  int64
			code, message string
		}{
			{1, 0, -1, TimestampPositiveInfinity, "22008", "origin out of range"},
			{0, 0, 0, TimestampNegativeInfinity, "22008", "origin out of range"},
			{math.MaxInt32, math.MaxInt32, math.MaxInt64, 0, "22008", "timestamps cannot be binned into infinite intervals"},
			{math.MinInt32, math.MinInt32, math.MinInt64, 0, "22008", "timestamps cannot be binned into infinite intervals"},
			{1, -30, 0, 0, "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{math.MaxInt32, 0, math.MaxInt64, 0, "0A000", "timestamps cannot be binned into intervals containing months or years"},
			{0, 0, 0, 0, "22008", "stride must be greater than zero"},
			{0, 0, -1, 0, "22008", "stride must be greater than zero"},
			{0, 1, -microsPerDay, 0, "22008", "stride must be greater than zero"},
		} {
			args := datetimeBinUnitArgs(d, id, tc.months, tc.days, tc.span, 1, tc.origin)
			_, err := p.Evaluate(context.Background(), args[:])
			datetimeBinUnitFailure(t, err, tc.code, tc.message)
			for _, infinity := range []int64{TimestampPositiveInfinity, TimestampNegativeInfinity} {
				args[1], _ = TimestampValue(d[id], infinity)
				v, err := p.Evaluate(context.Background(), args[:])
				if err != nil || v.integer != infinity || v.Type() != d[id] {
					t.Fatal(v, err)
				}
			}
		}
	}
}

func TestDatetimeBinOverflowEndpoints(t *testing.T) {
	d := datetimeMakeDescriptors()
	for _, id := range []ID{Timestamp, Timestamptz} {
		p, _ := BindDatetimeBin(d[Interval], d[id], d[id])
		for _, tc := range []struct {
			days                 int32
			span, source, origin int64
			message              string
		}{
			{200000000, 0, 1, 0, "interval out of range"},
			{106751992, -microsPerDay, 1, 0, "interval out of range"},
			{106751991, microsPerDay, 1, 0, "interval out of range"},
			{0, 1, timestampEnd, timestampMinimum, "interval out of range"},
			{0, math.MaxInt64, -3, -2, "timestamp out of range"},
			{0, 2, timestampMinimum, timestampMinimum + 1, "timestamp out of range"},
		} {
			args := datetimeBinUnitArgs(d, id, 0, tc.days, tc.span, tc.source, tc.origin)
			_, err := p.Evaluate(context.Background(), args[:])
			datetimeBinUnitFailure(t, err, "22008", tc.message)
		}
		for _, tc := range []struct{ span, source, origin, want int64 }{
			{1, timestampEnd, 0, timestampEnd}, {1, timestampMinimum, timestampMinimum + 1, timestampMinimum},
			{math.MaxInt64, 1, 0, 0}, {1, timestampEnd - 1, timestampEnd, timestampEnd - 1},
		} {
			args := datetimeBinUnitArgs(d, id, 0, 0, tc.span, tc.source, tc.origin)
			v, err := p.Evaluate(context.Background(), args[:])
			if err != nil || v.integer != tc.want {
				t.Fatal(tc, v, err)
			}
		}
	}
}

func TestDatetimeBinAllocations(t *testing.T) {
	d := datetimeMakeDescriptors()
	for _, id := range []ID{Timestamp, Timestamptz} {
		p, _ := BindDatetimeBin(d[Interval], d[id], d[id])
		args := datetimeBinUnitArgs(d, id, 0, 0, 15, -31, 0)
		for _, source := range []Value{args[1], Null(d[id]), {typ: d[id], integer: TimestampPositiveInfinity}} {
			args[1] = source
			ctx := context.Background()
			if n := testing.AllocsPerRun(100, func() {
				if _, err := p.Evaluate(ctx, args[:]); err != nil {
					panic(err)
				}
			}); n != 0 {
				t.Fatalf("bin allocations: %g", n)
			}
		}
	}
}
