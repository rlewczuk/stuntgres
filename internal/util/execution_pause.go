package util

import (
	"context"
	"errors"
	"sync"
)

type executionPauseKey struct{}
type executionPauseActiveKey struct{}

// ExecutionPause is a one-shot cooperative suspension of matching work.
// Its channels are immutable; mu serializes admission against early release.
// No caller code executes under mu, and checkpoints hold no runtime locks.
type ExecutionPause struct {
	point    string
	mu       sync.Mutex
	admitted chan struct{}
	released chan struct{}
	entered  bool
	resumed  bool
}

func NewExecutionPause(point string) *ExecutionPause {
	return &ExecutionPause{point: point, admitted: make(chan struct{}), released: make(chan struct{})}
}

func WithExecutionPause(ctx context.Context, pause *ExecutionPause) context.Context {
	return context.WithValue(ctx, executionPauseKey{}, pause)
}

// HasExecutionPause reports attachment, not permission to block. Detached
// invocation owners use it to hand off reservations before activating work.
func HasExecutionPause(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	p, _ := ctx.Value(executionPauseKey{}).(*ExecutionPause)
	return p != nil
}

// ActivateExecutionPause is internal admission for detached execution only.
// Attaching a public pause never grants permission to wait during binding or
// metadata capture. Callers activate once per detached evaluation, not per poll.
// A missing optional evaluation context remains missing; this helper never
// supplies cancellation authority on behalf of its caller.
func ActivateExecutionPause(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(executionPauseKey{}).(*ExecutionPause)
	if p == nil || ctx.Value(executionPauseActiveKey{}) == true {
		return ctx
	}
	return context.WithValue(ctx, executionPauseActiveKey{}, true)
}

// DeactivateExecutionPause preserves the request while removing execution
// permission at a binding/metadata boundary, including nested runtime reentry.
func DeactivateExecutionPause(ctx context.Context) context.Context {
	if ctx == nil || ctx.Value(executionPauseActiveKey{}) != true {
		return ctx
	}
	return context.WithValue(ctx, executionPauseActiveKey{}, false)
}

// Wait observes actual admission, not merely a request to suspend work.
// Resuming before admission terminates Wait without claiming work was entered.
func (p *ExecutionPause) Wait(ctx context.Context) error {
	select {
	case <-p.admitted:
		return nil
	case <-p.released:
		p.mu.Lock()
		entered := p.entered
		p.mu.Unlock()
		if entered {
			return nil
		}
		return errors.New("stuntgres: execution pause resumed before admission")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Resume releases every matching worker, including workers arriving later.
func (p *ExecutionPause) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.resumed {
		p.resumed = true
		close(p.released)
	}
}

// ExecutionCheckpoint cooperatively suspends only matching work. The context
// must be a non-nil effective operation child, and the caller must hold no runtime
// lock. Cancellation is owned entirely by that context, never by the pause.
// Unconfigured checkpoints allocate no state and launch no goroutines.
func ExecutionCheckpoint(ctx context.Context, point string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ctx.Value(executionPauseActiveKey{}) != true {
		return nil
	}
	p, _ := ctx.Value(executionPauseKey{}).(*ExecutionPause)
	if p == nil || p.point != point {
		return nil
	}
	p.mu.Lock()
	if p.resumed {
		p.mu.Unlock()
		return ctx.Err()
	}
	if !p.entered {
		p.entered = true
		close(p.admitted)
	}
	p.mu.Unlock()
	select {
	case <-p.released:
	case <-ctx.Done():
	}
	return ctx.Err()
}
