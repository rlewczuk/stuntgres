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
	Active      atomic.Bool
	Decoding    atomic.Bool
	Masked      context.Context
	cancel      context.CancelCauseFunc
	timerCancel context.CancelFunc
}

func (c *protocolCopyInputContext) Held() bool {
	return c.Active.Load() && c.Decoding.Load()
}

func pendingProtocolUserCancel(ctx context.Context) bool {
	var diagnostic *sqlstate.Error
	return ctx.Err() != nil && errors.As(context.Cause(ctx), &diagnostic) && diagnostic != nil && diagnostic.Code == "57014" && diagnostic.Severity != "FATAL"
}

// Caller holds Session.mu. The original operation context is never replaced.
func (c *protocolCopyInputContext) effectiveLocked(op *Operation) context.Context {
	if !c.Held() {
		return op.Ctx
	}
	if c.Masked == nil && pendingProtocolUserCancel(op.Ctx) {
		// WithoutCancel hides the original cancelCtxKey as well as Done/Err.
		// Re-project only the already captured absolute budget, never a fresh one.
		c.Masked, c.cancel = context.WithCancelCause(context.WithoutCancel(op.Ctx))
		if deadline, ok := op.Ctx.Deadline(); ok {
			cause := error(context.DeadlineExceeded)
			if !op.StatementDeadline.IsZero() && deadline.Equal(op.StatementDeadline) {
				cause = statementTimeoutCause
			}
			c.Masked, c.timerCancel = context.WithDeadlineCause(c.Masked, deadline, cause)
		}
		if op.Released || op.Session.State != live {
			var cause error
			if op.Session.Timeouts.Fatal != nil {
				cause = op.Session.Timeouts.Fatal
			}
			c.cancel(cause)
		}
	}
	if c.Masked != nil {
		return c.Masked
	}
	return op.Ctx
}

func (op *Operation) StopCopyInput() {
	op.Session.Mu.Lock()
	op.CopyInput.Active.Store(false)
	op.CopyInput.Decoding.Store(false)
	cancel, timerCancel := op.CopyInput.cancel, op.CopyInput.timerCancel
	op.CopyInput.Masked, op.CopyInput.cancel, op.CopyInput.timerCancel = nil, nil, nil
	op.Session.Mu.Unlock()
	if cancel != nil {
		cancel(nil)
	}
	if timerCancel != nil {
		timerCancel()
	}
}

// Successful conversion must observe pending cancellation before its row can
// leave the input stage. Failed conversion returns its actual primary error.
func (op *Operation) PendingCopyInputError() error {
	if !op.CopyInput.Active.Load() {
		return nil
	}
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if err := op.CopyInput.effectiveLocked(op).Err(); err != nil {
		return err
	}
	return op.Ctx.Err()
}
