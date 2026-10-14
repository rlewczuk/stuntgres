package functions

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type datetimeSleepUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*datetimeSleepUnitContext) (sqltype.Value, error)
}
type datetimeSleepUnitContext struct {
	context.Context
	input sqltype.InputEnvironment
}

func (c *datetimeSleepUnitContext) FunctionContext() context.Context {
	if c == nil {
		return nil
	}
	return c.Context
}
func (c *datetimeSleepUnitContext) FunctionInput() sqltype.InputEnvironment { return c.input }
func (*datetimeSleepUnitContext) FunctionPropertiesMatch(p bool, rule IntrinsicPropertyRule) bool {
	return p && rule == VolatileOnly
}
func (*datetimeSleepUnitContext) FunctionSleepArgumentPresent(arg datetimeSleepUnitExpression) bool {
	return arg != nil
}
func (c *datetimeSleepUnitContext) FunctionSleepClock(usage sqltype.Usage) (sqltype.Value, error) {
	input := c.input
	input.TransactionStart = time.Now().UTC().Truncate(time.Microsecond)
	return sqltype.CurrentDateTime(usage, input)
}

type datetimeSleepUnitArgument struct {
	Info[bool]
	value sqltype.Value
}

func (arg datetimeSleepUnitArgument) Eval(*datetimeSleepUnitContext) (sqltype.Value, error) {
	return arg.value, nil
}

// Match the established native active-provider admission witness, not a timer
// checkpoint count or callback installed in the production wait loop.
func datetimeSleepUnitAdmission(t *testing.T, done <-chan error, worker string) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatal("wait completed before active admission", err)
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			providerFrame, workerFrame := false, false
			for {
				frame, more := frames.Next()
				providerFrame = providerFrame || strings.Contains(frame.Function, "datetimeSleepWaitRemaining")
				workerFrame = workerFrame || strings.Contains(frame.Function, worker)
				if !more {
					break
				}
			}
			if providerFrame && workerFrame {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatal("active wait admission deadline")
}
func TestDatetimeSleepWait(t *testing.T) {
	for _, seconds := range []float64{0, -0.1, math.NaN(), math.Inf(-1)} {
		if err := datetimeSleepWait(&datetimeSleepUnitContext{Context: context.Background()}, seconds); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		seconds float64
		want    time.Duration
	}{
		{1e-12, time.Nanosecond}, {0.075, 75 * time.Millisecond}, {599.5, 599500 * time.Millisecond}, {600, 600 * time.Second}, {math.MaxFloat64, 600 * time.Second}, {math.Inf(1), 600 * time.Second},
	} {
		if got := datetimeSleepChunk(tc.seconds); got != tc.want {
			t.Fatalf("chunk %g: %s want %s", tc.seconds, got, tc.want)
		}
	}
	start := time.Now()
	if err := datetimeSleepWait(&datetimeSleepUnitContext{Context: context.Background()}, 0.015); err != nil || time.Since(start) < 15*time.Millisecond {
		t.Fatal("early wait", time.Since(start), err)
	}
	for _, seconds := range []float64{10, math.MaxFloat64, math.Inf(1)} {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- datetimeSleepWait(&datetimeSleepUnitContext{Context: ctx}, seconds) }()
		datetimeSleepUnitAdmission(t, done, "TestDatetimeSleepWait")
		cancel()
		select {
		case err := <-done:
			if err != context.Canceled {
				t.Fatal("cancel identity", err)
			}
		case <-time.After(time.Second):
			t.Fatal("active wait did not join")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := datetimeSleepWait(&datetimeSleepUnitContext{Context: ctx}, 0); err != context.Canceled {
		t.Fatal("precancel identity", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := datetimeSleepWait(&datetimeSleepUnitContext{Context: ctx}, 1); err != context.DeadlineExceeded {
		t.Fatal("deadline identity", err)
	}
	for _, tc := range []struct {
		target, now int64
		want        float64
	}{{1000001, 1000000, 0.000001}, {-1000000, -1000001, 0.000001}, {math.MaxInt64 - 1, math.MinInt64 + 1, 18446744073709.55}} {
		got := datetimeSleepDifference(tc.target, tc.now)
		if math.Abs(got-tc.want) > math.Max(1e-12, math.Abs(tc.want)*1e-15) {
			t.Fatalf("wide difference: %.18g want %.18g", got, tc.want)
		}
	}
	if !math.IsInf(datetimeSleepDifference(sqltype.TimestampPositiveInfinity, 0), 1) || !math.IsInf(datetimeSleepDifference(sqltype.TimestampNegativeInfinity, 0), -1) {
		t.Fatal("infinity arithmetic")
	}
}
func TestDatetimeSleepCalendar(t *testing.T) {
	types := formatUnitTypes()
	stamp, span := types[sqltype.Timestamptz], types[sqltype.Interval]
	calendar, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, stamp, span, stamp)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		zone, start, interval, want string
		seconds                     float64
	}{
		{"UTC", "2025-01-31 12:00+00", "1 month", "2025-02-28T12:00:00Z", 28 * 86400},
		{"UTC", "2024-01-31 12:00+00", "1 month", "2024-02-29T12:00:00Z", 29 * 86400},
		{"America/New_York", "2025-03-08 12:00-05", "1 day", "2025-03-09T16:00:00Z", 23 * 3600},
		{"America/New_York", "2025-03-08 12:00-05", "24 hours", "2025-03-09T17:00:00Z", 24 * 3600},
		{"America/New_York", "2025-11-01 12:00-04", "1 day", "2025-11-02T17:00:00Z", 25 * 3600},
		{"America/New_York", "2025-03-08 12:00-05", "1 day -24 hours", "2025-03-08T16:00:00Z", -3600},
		{"America/New_York", "2025-11-01 12:00-04", "1 day -24 hours", "2025-11-01T17:00:00Z", 3600},
	} {
		zone, err := sqltype.ResolveZone(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		input := sqltype.InputEnvironment{Zone: zone}
		start, err := stamp.InputWithEnvironment(ctx, tc.start, -1, input)
		if err != nil {
			t.Fatal(err)
		}
		interval, err := span.Input(ctx, tc.interval, -1)
		if err != nil {
			t.Fatal(err)
		}
		end, err := calendar.Binary(ctx, start, interval, input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := end.TimestampTime()
		want, parseErr := time.Parse(time.RFC3339, tc.want)
		if err != nil || parseErr != nil || !got.Equal(want) {
			t.Fatal(tc, got, err, parseErr)
		}
		a, _ := start.Timestamp()
		z, _ := end.Timestamp()
		if seconds := datetimeSleepDifference(z, a); seconds != tc.seconds {
			t.Fatal(tc, "elapsed", seconds)
		}
		// The wrapper subtracts a second clock sample, not the first sample.
		if seconds := datetimeSleepDifference(z, a+250000); seconds != tc.seconds-0.25 {
			t.Fatal("fresh sample", seconds)
		}
	}
	start, _ := stamp.Input(context.Background(), "2025-01-01+00", -1)
	interval, _ := span.Input(context.Background(), "2147483647 months", -1)
	_, err = calendar.Binary(context.Background(), start, interval, sqltype.InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22008" || diagnostic.Message != "timestamp out of range" {
		t.Fatal("calendar overflow", err)
	}
}
func TestDatetimeSleepZeroDelayAllocations(t *testing.T) {
	types := formatUnitTypes()
	value, err := sqltype.Float(types[sqltype.Float8], 0)
	if err != nil {
		t.Fatal(err)
	}
	arg := datetimeSleepUnitArgument{Info: Info[bool]{Usage: types[sqltype.Float8].Usage()}, value: value}
	node, err := NewDatetimeSleep[datetimeSleepUnitExpression, *datetimeSleepUnitContext](Info[bool]{Usage: types[sqltype.Void].Usage(), Property: true}, DatetimeSleepSeconds, arg, types[sqltype.Timestamptz].Usage())
	if err != nil {
		t.Fatal(err)
	}
	ctx := &datetimeSleepUnitContext{Context: context.Background()}
	if allocations := testing.AllocsPerRun(10, func() {
		if _, err := node.Eval(ctx); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("zero-delay Eval allocation", allocations)
	}
}
