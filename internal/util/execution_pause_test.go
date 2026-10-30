package util

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestExecutionPauseOptionalContext(t *testing.T) {
	var optional context.Context
	if got := ActivateExecutionPause(optional); got != nil {
		t.Fatal("activation invented optional context", got)
	}
	if got := DeactivateExecutionPause(optional); got != nil {
		t.Fatal("deactivation invented optional context", got)
	}
	if got := DeactivateExecutionPause(ActivateExecutionPause(optional)); got != nil {
		t.Fatal("optional context composition invented authority", got)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	if ActivateExecutionPause(ctx) != ctx || DeactivateExecutionPause(ctx) != ctx {
		t.Fatal("unconfigured context identity changed")
	}
	cause := errors.New("owned optional context cause")
	cancel(cause)
	if got := ActivateExecutionPause(ctx); got != ctx || got.Err() != context.Canceled || context.Cause(got) != cause {
		t.Fatal("unconfigured cancellation authority changed")
	}
}

func TestExecutionPauseChildCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cause", true: "deadline"}[deadline], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				type key struct{}
				pause := NewExecutionPause("work")
				defer pause.Resume()
				source := WithExecutionPause(context.WithValue(context.Background(), key{}, "value"), pause)
				child, cancel := context.WithCancelCause(source)
				defer cancel(nil)
				cause := errors.New("child-only cause")
				var effective context.Context = child
				if deadline {
					var stop context.CancelFunc
					effective, stop = context.WithDeadlineCause(child, time.Now().Add(time.Second), cause)
					defer stop()
				}
				owned := effective
				wantDeadline, wantOK := owned.Deadline()
				effective = ActivateExecutionPause(owned)
				if effective.Done() != owned.Done() || effective.Value(key{}) != "value" || context.Cause(effective) != context.Cause(owned) {
					t.Fatal("changed child delegation")
				}
				done := make(chan error, 1)
				go func() { done <- ExecutionCheckpoint(effective, "work") }()
				if err := pause.Wait(context.Background()); err != nil {
					t.Fatal(err)
				}
				if deadline {
					<-effective.Done()
				} else {
					cancel(cause)
				}
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
				}
				if err := <-done; err != want || context.Cause(effective) != cause {
					t.Fatal("child cancellation", err, context.Cause(effective))
				}
				if source.Err() != nil {
					t.Fatal("child canceled source")
				}
				gotDeadline, gotOK := effective.Deadline()
				if gotDeadline != wantDeadline || gotOK != wantOK {
					t.Fatal("changed deadline")
				}
			})
		})
	}
}

func TestExecutionPauseLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pause := NewExecutionPause("work")
	defer pause.Resume()
	request := WithExecutionPause(ctx, pause)
	if err := ExecutionCheckpoint(request, "work"); err != nil {
		t.Fatal("binding-only request", err)
	}
	work := ActivateExecutionPause(request)
	if err := ExecutionCheckpoint(DeactivateExecutionPause(work), "work"); err != nil {
		t.Fatal("masked binding request", err)
	}
	if err := ExecutionCheckpoint(work, "other"); err != nil {
		t.Fatal(err)
	}
	if err := ExecutionCheckpoint(WithExecutionPause(work, nil), "work"); err != nil {
		t.Fatal(err)
	}
	if err := ExecutionCheckpoint(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	done := make(chan error, workers)
	for range workers {
		go func() { done <- ExecutionCheckpoint(work, "work") }()
	}
	if err := pause.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	pause.Resume()
	pause.Resume()
	for range workers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := pause.Wait(ctx); err != nil {
		t.Fatal("lost admitted state", err)
	}
	if err := ExecutionCheckpoint(work, "work"); err != nil {
		t.Fatal("future work", err)
	}
	early := NewExecutionPause("work")
	early.Resume()
	if err := early.Wait(ctx); err == nil {
		t.Fatal("early resume fabricated admission")
	}
	if err := ExecutionCheckpoint(WithExecutionPause(ctx, early), "work"); err != nil {
		t.Fatal(err)
	}
	never := NewExecutionPause("absent")
	defer never.Resume()
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := never.Wait(canceled); err != context.Canceled {
		t.Fatal("Wait cancellation", err)
	}
	if err := ExecutionCheckpoint(canceled, "work"); err != context.Canceled {
		t.Fatal("pre-canceled checkpoint", err)
	}
}

func TestExecutionPauseUnconfiguredAllocations(t *testing.T) {
	ctx := context.Background()
	allocations, bytes := AllocationsPerCall(100, func() {
		execution := ActivateExecutionPause(ctx)
		binding := DeactivateExecutionPause(execution)
		if err := ExecutionCheckpoint(binding, "work"); err != nil {
			panic(err)
		}
	})
	if allocations != 0 || bytes != 0 {
		t.Fatal("unconfigured checkpoint allocations", allocations, bytes)
	}
}
