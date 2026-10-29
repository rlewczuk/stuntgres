package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type aggregateGateWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *aggregateGateWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestAggregateCombineSQLGate(t *testing.T) {
	_, p, evaluation, _ := aggregateCombineUnit(t)
	s := core.ScopeOf(evaluation.Host)
	gate := core.ScopeAggregateSQLGate(s)
	gate <- struct{}{}
	base, cancel := context.WithTimeout(exec.ParallelWorkerContext(evaluation.Context), 5*time.Second)
	defer cancel()
	ctx := &aggregateGateWaitContext{Context: base, entered: make(chan struct{})}
	local := *evaluation
	local.Context = ctx
	state, err := p.InitWithContext(ctx, &local)
	if err != nil {
		t.Fatal(err)
	}
	one, err := sqltype.Integer(p.Inputs[1].Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertWaitingOwner := core.CaptureAggregateScratchOwnership(s)
	done := make(chan error, 1)
	go func() { done <- p.Advance(ctx, state, []sqltype.Value{one}, &local) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
		<-gate
	}()
	select {
	case <-ctx.entered: // The actual host gate is blocked, not a timing guess.
	case <-base.Done():
		t.Fatal("SQL gate wait not reached", base.Err())
	}
	cancel()
	err = <-done
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if core.ScopeActiveSQLRoutineDepth(s) != 0 || core.ScopeAggregateInvocationContext(s) != nil {
		t.Fatal("canceled waiter mutated SQL host")
	}
	if err := assertWaitingOwner(); err != nil {
		t.Fatal(err)
	}
	value, err := p.Finalize(evaluation.Context, state)
	if err != nil || !value.IsNull() {
		t.Fatal("canceled waiter published state", value, err)
	}
	// Release the artificially retained gate, then prove the same live owner
	// and provider can enter normally. Restore the token for balanced cleanup.
	<-gate
	if err := p.Advance(evaluation.Context, state, []sqltype.Value{one}, evaluation); err != nil {
		gate <- struct{}{}
		t.Fatal(err)
	}
	gate <- struct{}{}
	value, err = p.Finalize(evaluation.Context, state)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 1 {
		t.Fatal(n, err, integerErr)
	}
	t.Run("worker_initialization_snapshot", aggregateCombineWorkerInitializationSnapshot)
}
