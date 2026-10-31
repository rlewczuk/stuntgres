package core_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDatetimeSleepBinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   sqltype.ID
	}{{"pg_sleep", sqltype.Float8}, {"pg_sleep_for", sqltype.Interval}, {"pg_sleep_until", sqltype.Timestamptz}} {
		b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
		x, err := b.BindFunction(datetimeMakeRaw(tc.name, parser.ParameterExpr{Number: 1}))
		if err != nil {
			t.Fatal(err)
		}
		n := x.(functions.DatetimeSleep)
		if n.ResultUsage().Type.ID() != sqltype.Void || n.Properties().Volatility != expr.Volatile || !n.Properties().ParameterDependent || b.Params.Parameters[1].Type.ID() != tc.id || !n.Validate(expr.PropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) {
			t.Fatalf("binding %s: %#v", tc.name, n)
		}
		if plan.GenerationImmutable(n) {
			t.Fatal("sleep admitted as immutable")
		}
		b = enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
		x, err = b.BindFunction(datetimeMakeRaw(tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}))
		if err != nil {
			t.Fatal(err)
		}
		folded, changed, err := exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, x)
		if err != nil || !changed {
			t.Fatal(folded, changed, err)
		}
		v, err := folded.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || !v.IsNull() || v.Type().ID() != sqltype.Void {
			t.Fatal(v, err)
		}
	}
}

func TestDatetimeSleepCleanup(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		var ref ids.TxRef
		if explicit {
			ref = enginetest.TransactionBegin(t, s)
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
		if err := scope.Gate(&parser.Select{}); err != nil {
			scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		binder := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		wait, err := binder.BindFunction(datetimeMakeRaw("pg_sleep", ageFiniteTyped("float8", "Infinity")))
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := wait.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope)}})
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
		if !scope.Finished || scope.View != nil || scope.LockTicket != nil || scope.Stage.Rows != nil {
			t.Fatal("execution resources retained")
		}
		if explicit {
			if s.TransactionState() != session.TxAborted || s.Transactions.Ticket != nil {
				t.Fatal("explicit abort owner retained")
			}
			enginetest.TransactionEnd(t, s, ref, false)
		} else if s.TransactionState() != session.TxIdle {
			t.Fatal("implicit owner retained")
		}
		op = enginetest.TransactionOperation(t, s)
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
