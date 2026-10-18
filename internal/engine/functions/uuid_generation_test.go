package functions

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type uuidUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *uuidUnitContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}
func (c *uuidUnitContext) FunctionInput() sqltype.InputEnvironment {
	panic("unexpected UUID calendar demand")
}
func (c *uuidUnitContext) FunctionUUIDReady() error {
	return fmt.Errorf("stuntgres: missing UUID session scope")
}
func (c *uuidUnitContext) FunctionUUIDTime() int64          { panic("unexpected UUID clock demand") }
func (c *uuidUnitContext) FunctionUUIDEntropy([]byte) error { panic("unexpected UUID entropy demand") }

type uuidUnitArgument struct {
	expr.Info
	usage sqltype.Usage
	value sqltype.Value
	calls *int
}

func (a uuidUnitArgument) ResultUsage() sqltype.Usage { return a.usage }
func (a uuidUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	*a.calls++
	return a.value, nil
}
func TestUUIDGenerationRuntimeNullDemand(t *testing.T) {
	types := formatUnitTypes()
	iv, uuid := types[sqltype.Interval], types[sqltype.UUID]
	calls := 0
	probe := uuidUnitArgument{usage: iv.Usage(), value: sqltype.Null(iv), calls: &calls}
	node := UUIDGeneration{Info: expr.Info{Usage: uuid.Usage()}, Name: "uuidv7", Args: []expr.Expr{probe}}
	result, err := node.Eval(&uuidUnitContext{Context: context.Background()})
	if err != nil || !result.IsNull() || result.Type() != uuid || calls != 1 {
		t.Fatal(result, err, calls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = node.Eval(&uuidUnitContext{Context: canceled})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancellation demanded child", err, calls)
	}
}
func uuidUnitBytes(t *testing.T, text string) [16]byte {
	t.Helper()
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != 16 {
		t.Fatal(text, err)
	}
	var result [16]byte
	copy(result[:], raw)
	return result
}
func TestUUIDGenerationPacking(t *testing.T) {
	zero := [16]byte{}
	var full [16]byte
	for i := range full {
		full[i] = 255
	}
	for _, tc := range []struct {
		input [16]byte
		want  string
	}{{zero, "00000000000040008000000000000000"}, {full, "ffffffffffff4fffbfffffffffffffff"}} {
		if got := uuidPackV4(tc.input); got != uuidUnitBytes(t, tc.want) {
			t.Fatalf("v4 got %x want %s", got, tc.want)
		}
	}
	for _, tc := range []struct {
		ms      uint64
		ns      int64
		entropy [16]byte
		want    string
	}{
		{0, 500000, zero, "00000000000078008000000000000000"},
		{1, 0, zero, "00000000000170008000000000000000"},
		{0x010203040506, 0, full, "0102030405067000bfffffffffffffff"},
		{0xffffffffffff, 999999, zero, "ffffffffffff7fff8000000000000000"},
	} {
		if got := uuidPackV7(tc.ms, tc.ns, tc.entropy, false); got != uuidUnitBytes(t, tc.want) {
			t.Fatalf("v7 got %x want %s", got, tc.want)
		}
	}
	for _, tc := range []struct {
		ns   int64
		want uint16
	}{{0, 0}, {244, 0}, {245, 1}, {999999, 4095}} {
		got := uuidPackV7(0, tc.ns, zero, false)
		fraction := uint16(got[6]&15)<<8 | uint16(got[7])
		if fraction != tc.want {
			t.Fatal(tc, fraction)
		}
	}
	for bits := range 4 {
		entropy := zero
		entropy[8] = byte(bits << 6)
		got := uuidPackV7(0, 0, entropy, true)
		if got[7] != byte(bits) || got[8] != 0x80 {
			t.Fatal("coarse fraction", bits, got)
		}
	}
}
func TestUUIDGenerationAscendingClock(t *testing.T) {
	for _, step := range []int64{245, 977} {
		for _, tc := range []struct{ previous, observed, want int64 }{{1000, 1000, 1000 + step}, {1000, 999, 1000 + step}, {1000, 1000 + step, 1000 + step}, {1000, 3000, 3000}, {999999, 1000000, 999999 + step}} {
			if got := UUIDAscendingTime(tc.previous, tc.observed, step); got != tc.want {
				t.Fatal(step, tc, got)
			}
		}
	}
	if got := uuidPackV7(1, 244, [16]byte{}, false); got[5] != 1 || got[7] != 0 {
		t.Fatal("millisecond carry", got)
	}
}
func TestUUIDGenerationExactShiftBounds(t *testing.T) {
	for _, tc := range []struct {
		us    int64
		valid bool
		ms    uint64
	}{{0, true, 0}, {-1, false, 0}, {uuidMaxUnixMicroseconds, true, 0xffffffffffff}, {uuidMaxUnixMicroseconds + 1, false, 0}} {
		ms, ns, err := uuidShiftTimestamp(tc.us-uuidEpochMicroseconds, 999)
		if tc.valid {
			if err != nil || ms != tc.ms || ns != 999 {
				t.Fatal(tc, ms, ns, err)
			}
		} else {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22008", Message: "timestamp out of range for UUID version 7", Detail: "UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889."})
		}
	}
	ms, ns, err := uuidShiftTimestamp(1001-uuidEpochMicroseconds, 321)
	if err != nil || ms != 1 || ns != 1321 {
		t.Fatal("submicrosecond preservation", ms, ns, err)
	}
	for _, tc := range []struct {
		m, d int32
		us   int64
		want bool
	}{{math.MinInt32, math.MinInt32, math.MinInt64, true}, {math.MaxInt32, math.MaxInt32, math.MaxInt64, true}, {math.MinInt32, 0, 0, false}, {0, math.MaxInt32, 0, false}, {0, 0, math.MaxInt64, false}} {
		if uuidInfiniteInterval(tc.m, tc.d, tc.us) != tc.want {
			t.Fatal(tc)
		}
	}
	// Even int64 extrema reject cleanly rather than overflowing epoch arithmetic.
	for _, us := range []int64{math.MinInt64, math.MaxInt64} {
		if _, _, err := uuidShiftTimestamp(us, 0); err == nil {
			t.Fatal("extreme timestamp admitted", us)
		}
	}
}
func TestUUIDGenerationCalendarProvider(t *testing.T) {
	types := formatUnitTypes()
	stamp := types[sqltype.Timestamptz]
	iv := types[sqltype.Interval]
	addition, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, stamp, iv, stamp)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		start, want time.Time
		m, d        int32
		us          int64
		zone        string
	}{
		{time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), 1, 0, 0, "UTC"},
		{time.Date(2023, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2023, 3, 1, 12, 0, 0, 0, time.UTC), 1, 1, 0, "UTC"},
		{time.Date(2005, 4, 2, 19, 0, 0, 0, time.UTC), time.Date(2005, 4, 3, 18, 0, 0, 0, time.UTC), 0, 1, 0, "America/Denver"},
		{time.Date(2005, 4, 2, 19, 0, 0, 0, time.UTC), time.Date(2005, 4, 3, 19, 0, 0, 0, time.UTC), 0, 0, 86400000000, "America/Denver"},
		{time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 28, 14, 0, 0, 0, time.UTC), 1, -1, 7200000000, "UTC"},
	} {
		a, err := sqltype.TimestampFromTime(stamp, tc.start)
		if err != nil {
			t.Fatal(err)
		}
		shift, err := sqltype.IntervalValue(iv, tc.m, tc.d, tc.us)
		if err != nil {
			t.Fatal(err)
		}
		zone, err := sqltype.ResolveZone(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		value, err := addition.Binary(context.Background(), a, shift, sqltype.InputEnvironment{Zone: zone})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := value.TimestampTime()
		if err != nil || !actual.Equal(tc.want) {
			t.Fatal(tc, actual, err)
		}
	}
	a, _ := sqltype.TimestampFromTime(stamp, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	shift, _ := sqltype.IntervalValue(iv, math.MaxInt32, 0, 0)
	_, err = addition.Binary(context.Background(), a, shift, sqltype.InputEnvironment{})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22008", Message: "timestamp out of range"})
	// Large valid UUID times pack directly from microseconds, never UnixNano.
	ms, ns, err := uuidShiftTimestamp(33000000000000000-uuidEpochMicroseconds, 123)
	if err != nil {
		t.Fatal(err)
	}
	got := uuidPackV7(ms, ns, [16]byte{}, false)
	if bytes.Equal(got[:6], make([]byte, 6)) || ms != 33000000000000 {
		t.Fatal("far future encoding", got, ms)
	}
}
