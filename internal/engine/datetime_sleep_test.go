package engine

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatetimeSleepBinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   sqltype.ID
	}{{"pg_sleep", sqltype.Float8}, {"pg_sleep_for", sqltype.Interval}, {"pg_sleep_until", sqltype.Timestamptz}} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		x, err := b.bindFunction(datetimeMakeRaw(tc.name, parser.ParameterExpr{Number: 1}))
		if err != nil {
			t.Fatal(err)
		}
		n := x.(datetimeSleepExpression)
		if n.ResultUsage().Type.ID() != sqltype.Void || n.Properties().Volatility != Volatile || !n.Properties().ParameterDependent || b.parameters[1].Type.ID() != tc.id || !n.validStored() {
			t.Fatalf("binding %s: %#v", tc.name, n)
		}
		if generationImmutable(n) {
			t.Fatal("sleep admitted as immutable")
		}
		b = newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		x, err = b.bindFunction(datetimeMakeRaw(tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}))
		if err != nil {
			t.Fatal(err)
		}
		folded, changed, err := preflightCoercions(&EvalContext{Context: context.Background()}, x)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		v, err := folded.Eval(&EvalContext{Context: context.Background()})
		if err != nil || !v.IsNull() || v.Type().ID() != sqltype.Void {
			t.Fatal(v, err)
		}
	}
}

func TestDatetimeSleepWait(t *testing.T) {
	for _, seconds := range []float64{0, -0.1, math.NaN(), math.Inf(-1)} {
		if err := datetimeSleepWait(context.Background(), seconds); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		seconds float64
		want    time.Duration
	}{{1e-12, time.Nanosecond}, {0.075, 75 * time.Millisecond}, {599.5, 599500 * time.Millisecond}, {600, 600 * time.Second}, {math.MaxFloat64, 600 * time.Second}, {math.Inf(1), 600 * time.Second}} {
		if got := datetimeSleepChunk(tc.seconds); got != tc.want {
			t.Fatalf("chunk %g: %s want %s", tc.seconds, got, tc.want)
		}
	}
	start := time.Now()
	if err := datetimeSleepWait(context.Background(), 0.015); err != nil || time.Since(start) < 15*time.Millisecond {
		t.Fatal("early wait", time.Since(start), err)
	}
	for _, seconds := range []float64{10, math.MaxFloat64, math.Inf(1)} {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			first := true
			done <- datetimeSleepWaitRemaining(ctx, func() (float64, error) {
				if first {
					close(entered)
					first = false
				}
				return seconds, nil
			})
		}()
		<-entered
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
	if err := datetimeSleepWait(ctx, 0); err != context.Canceled {
		t.Fatal("precancel identity", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := datetimeSleepWait(ctx, 1); err != context.DeadlineExceeded {
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
	e := New()
	stamp, _ := e.CatalogRead().LookupType(sqltype.Timestamptz)
	span, _ := e.CatalogRead().LookupType(sqltype.Interval)
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
		c := &EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: zone}}
		start, err := stamp.InputWithEnvironment(c.Context, tc.start, -1, c.Input)
		if err != nil {
			t.Fatal(err)
		}
		interval, err := span.Input(c.Context, tc.interval, -1)
		if err != nil {
			t.Fatal(err)
		}
		end, err := calendar.Binary(c.Context, start, interval, c.Input)
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
	oldTransaction := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{TransactionStart: oldTransaction}}
	before := time.Now().UTC().Truncate(time.Microsecond)
	current, err := datetimeSleepClock(c, stamp.Usage())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	instant, err := current.TimestampTime()
	if err != nil || instant.Before(before) || instant.After(after) || c.Input.TransactionStart != oldTransaction {
		t.Fatal("clock used/mutated transaction start", instant, err)
	}
	start, _ := stamp.Input(context.Background(), "2025-01-01+00", -1)
	interval, _ := span.Input(context.Background(), "2147483647 months", -1)
	_, err = calendar.Binary(context.Background(), start, interval, sqltype.InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22008" || diagnostic.Message != "timestamp out of range" {
		t.Fatal("calendar overflow", err)
	}
}

func TestDatetimeSleepCleanup(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := New()
		s := sessionStart(t, e, nil)
		var ref TxRef
		if explicit {
			ref = transactionBegin(t, s)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(op.Done)
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(context.Canceled) })
		if err := scope.gate(&parser.Select{}); err != nil {
			scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		entered := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			first := true
			done <- datetimeSleepWaitRemaining(ctx, func() (float64, error) {
				if first {
					close(entered)
					first = false
				}
				return math.Inf(1), nil
			})
		}()
		<-entered
		cancel()
		select {
		case err = <-done:
		case <-time.After(time.Second):
			t.Fatal("admitted wait did not join")
		}
		if err != context.Canceled || scope.Finish(err) != context.Canceled || op.CommandError(err) != context.Canceled {
			t.Fatal("finish original cause", err)
		}
		op.Done()
		if !scope.finished || scope.view != nil || scope.ticket != nil || scope.stage.rows != nil {
			t.Fatal("execution resources retained")
		}
		if explicit {
			if s.TransactionState() != TxAborted || s.transactions.ticket != nil {
				t.Fatal("explicit abort owner retained")
			}
			transactionEnd(t, s, ref, false)
		} else if s.TransactionState() != TxIdle {
			t.Fatal("implicit owner retained")
		}
		op = transactionOperation(t, s)
		scope, err = e.StartStatement(op)
		if err != nil {
			t.Fatal("healthy next scope", err)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
	}
}

func TestDatetimeSleepStored(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := b.bindFunction(datetimeMakeRaw("pg_sleep", datetimeMakeInteger(0)))
	if err != nil {
		t.Fatal(err)
	}
	n := node.(datetimeSleepExpression)
	if !closedTransactionTimeDefault(n) {
		t.Fatal("closed sleep default rejected")
	}
	target := New()
	rebound, _, err := retagBoundTypes(target.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil {
		t.Fatal("retag", err)
	}
	copy := rebound.(datetimeSleepExpression)
	value, err := copy.Eval(&EvalContext{Context: context.Background()})
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("reminted zero delay", value, err)
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), copy); err == nil {
		t.Fatal("foreign generation accepted")
	}
	bad := n
	bad.properties.ParameterDependent = true
	if closedTransactionTimeDefault(bad) {
		t.Fatal("dependent default accepted")
	}
	interval, err := b.bindFunction(datetimeMakeRaw("pg_sleep_for", ageFiniteTyped("interval", "0")))
	if err != nil {
		t.Fatal(err)
	}
	rebound, _, err = retagBoundTypes(target.CatalogRead(), interval, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil {
		t.Fatal("interval retag", err)
	}
	value, err = rebound.Eval(&EvalContext{Context: context.Background()})
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("reminted calendar zero delay", value, err)
	}
}
