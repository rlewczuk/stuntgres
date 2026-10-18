package engine

import (
	"context"
	"runtime"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
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
		n := x.(functions.DatetimeSleep)
		if n.ResultUsage().Type.ID() != sqltype.Void || n.Properties().Volatility != expr.Volatile || !n.Properties().ParameterDependent || b.parameters[1].Type.ID() != tc.id || !n.Validate(expr.PropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) {
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

func TestDatetimeSleepClockOwner(t *testing.T) {
	e := New()
	stamp, _ := e.CatalogRead().LookupType(sqltype.Timestamptz)
	oldTransaction := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{TransactionStart: oldTransaction}}
	before := time.Now().UTC().Truncate(time.Microsecond)
	current, err := c.FunctionSleepClock(stamp.Usage())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	instant, err := current.TimestampTime()
	if err != nil || instant.Before(before) || instant.After(after) || c.Input.TransactionStart != oldTransaction {
		t.Fatal("clock used/mutated transaction start", instant, err)
	}
}

func TestDatetimeSleepCleanup(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := New()
		s := sessionStart(t, e, nil)
		var ref ids.TxRef
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
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		wait, err := binder.bindFunction(datetimeMakeRaw("pg_sleep", ageFiniteTyped("float8", "Infinity")))
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := wait.Eval(&EvalContext{Context: ctx, statementScope: scope})
			done <- err
		}()
		datetimeSleepCleanupAdmission(t, done)
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
	n := node.(functions.DatetimeSleep)
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
	copy := rebound.(functions.DatetimeSleep)
	value, err := copy.Eval(&EvalContext{Context: context.Background()})
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("reminted zero delay", value, err)
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), copy); err == nil {
		t.Fatal("foreign generation accepted")
	}
	bad := n
	bad.Property.ParameterDependent = true
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

// Use the established native active-provider profile witness: this retained
// integration exercises the real carrier and admitted statement, not a callback
// or a test-only access path into the child's private wait implementation.
func datetimeSleepCleanupAdmission(t *testing.T, done <-chan error) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatal("admitted wait completed before active admission", err)
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
				workerFrame = workerFrame || strings.Contains(frame.Function, "TestDatetimeSleepCleanup")
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
	t.Fatal("admitted wait admission deadline")
}
