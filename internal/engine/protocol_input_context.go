package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"sync/atomic"
)

// Only real wire COPY input activates this view. Ordinary operations and input
// without an already-pending typed SQL cancellation keep their original context.
// Session.mu protects the lazy context and its cancellation. Context returns an
// actual standard context, never a proxy that changes Done/Err after selection.
type protocolCopyInputContext struct {
	active      atomic.Bool
	decoding    atomic.Bool
	masked      context.Context
	cancel      context.CancelCauseFunc
	timerCancel context.CancelFunc
}

func (c *protocolCopyInputContext) held() bool {
	return c.active.Load() && c.decoding.Load()
}

func pendingProtocolUserCancel(ctx context.Context) bool {
	var diagnostic *sqlstate.Error
	return ctx.Err() != nil && errors.As(context.Cause(ctx), &diagnostic) && diagnostic != nil && diagnostic.Code == "57014" && diagnostic.Severity != "FATAL"
}

// Caller holds Session.mu. The original operation context is never replaced.
func (c *protocolCopyInputContext) effectiveLocked(op *Operation) context.Context {
	if !c.held() {
		return op.ctx
	}
	if c.masked == nil && pendingProtocolUserCancel(op.ctx) {
		// WithoutCancel hides the original cancelCtxKey as well as Done/Err.
		// Re-project only the already captured absolute budget, never a fresh one.
		c.masked, c.cancel = context.WithCancelCause(context.WithoutCancel(op.ctx))
		if deadline, ok := op.ctx.Deadline(); ok {
			cause := error(context.DeadlineExceeded)
			if !op.statementDeadline.IsZero() && deadline.Equal(op.statementDeadline) {
				cause = statementTimeoutCause
			}
			c.masked, c.timerCancel = context.WithDeadlineCause(c.masked, deadline, cause)
		}
		if op.done || op.session.state != live {
			var cause error
			if op.session.timeouts.fatal != nil {
				cause = op.session.timeouts.fatal
			}
			c.cancel(cause)
		}
	}
	if c.masked != nil {
		return c.masked
	}
	return op.ctx
}

func (op *Operation) stopCopyInput() {
	op.session.mu.Lock()
	op.copyInput.active.Store(false)
	op.copyInput.decoding.Store(false)
	cancel, timerCancel := op.copyInput.cancel, op.copyInput.timerCancel
	op.copyInput.masked, op.copyInput.cancel, op.copyInput.timerCancel = nil, nil, nil
	op.session.mu.Unlock()
	if cancel != nil {
		cancel(nil)
	}
	if timerCancel != nil {
		timerCancel()
	}
}

// Successful conversion must observe pending cancellation before its row can
// leave the input stage. Failed conversion returns its actual primary error.
func (op *Operation) pendingCopyInputError() error {
	if !op.copyInput.active.Load() {
		return nil
	}
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if err := op.copyInput.effectiveLocked(op).Err(); err != nil {
		return err
	}
	return op.ctx.Err()
}
