package dual

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Synthesized reference SQLSTATEs, as the cmd/stuntgres dual backend uses them.
const (
	codeFailed = "08001" // the reference connection could not be opened
	codeLost   = "08006" // the reference connection was lost or closed
)

// pairRef is the paired reference connection of one dual connection. Failed
// and lost are final: the connection is never reopened, because a new session
// would lack the state the native session kept.
type pairRef struct {
	conn    driver.Conn // nil unless healthy
	code    string      // "" healthy, codeFailed or codeLost
	message string      // failure message of a failed or lost pair
}

func (p *pairRef) fail(message string) { p.code, p.message = codeFailed, message }

// lose marks the pair lost. closeConn closes the reference connection in the
// background; it is false when an abandoned task still owns the connection.
func (p *pairRef) lose(message string, closeConn bool) {
	rc := p.conn
	p.conn, p.code, p.message = nil, codeLost, message
	if closeConn && rc != nil {
		go closeQuietly(rc)
	}
}

// task is the outcome of one bounded reference task.
type task[T any] struct {
	value     T
	err       error
	panicked  any                // recovered panic value
	abandoned bool               // the timeout expired before the call returned
	cancel    context.CancelFunc // kept context's cancel (keep requested, call succeeded)
	duration  time.Duration
}

// handoff decides, under its leaf mutex, whether a finished task result goes
// to the waiter or the waiter has abandoned the task.
type handoff struct {
	mu        sync.Mutex
	finished  bool
	abandoned bool
}

// runTask runs call in its own goroutine with a context that ignores the
// caller's cancellation and deadline, and waits at most timeout. On expiry it
// cancels that context and returns at once; the goroutine then passes the late
// result to abandon. keep retains the context after a successful call (a
// reference transaction watches its BeginTx context until it ends).
func runTask[T any](parent context.Context, timeout time.Duration, keep bool, call func(context.Context) (T, error), abandon func(T)) task[T] {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	start := time.Now()
	done := make(chan task[T], 1)
	h := &handoff{}
	go func() {
		var t task[T]
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.panicked = r
				}
			}()
			t.value, t.err = call(ctx)
		}()
		h.mu.Lock()
		gone := h.abandoned
		h.finished = true
		h.mu.Unlock()
		if !gone {
			done <- t
			return
		}
		if abandon != nil {
			func() {
				defer func() { _ = recover() }()
				abandon(t.value)
			}()
		}
		cancel()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var t task[T]
	select {
	case t = <-done:
	case <-timer.C:
		h.mu.Lock()
		finished := h.finished
		h.abandoned = !finished
		h.mu.Unlock()
		if !finished {
			cancel()
			return task[T]{abandoned: true, duration: time.Since(start)}
		}
		t = <-done
	}
	t.duration = time.Since(start)
	if keep && t.panicked == nil && t.err == nil {
		t.cancel = cancel
	} else {
		cancel()
	}
	return t
}

// refOutcome is the outcome of one call on the paired reference connection.
type refOutcome[T any] struct {
	value    T
	err      error
	cancel   context.CancelFunc
	failure  string // non-empty: this call failed the pair (lost)
	duration time.Duration
}

// refDo runs call on the healthy paired reference connection, bounded by the
// timeout, and applies the lost transitions: timeout, panic, a bad-connection
// error, or an error after which the connection is no longer valid.
func refDo[T any](c *conn, ctx context.Context, keep bool, call func(context.Context, driver.Conn) (T, error)) refOutcome[T] {
	rc := c.ref.conn
	timeout := c.cn.opts.ReferenceTimeout
	t := runTask(ctx, timeout, keep, func(ctx context.Context) (T, error) { return call(ctx, rc) }, func(T) { closeQuietly(rc) })
	out := refOutcome[T]{value: t.value, err: t.err, cancel: t.cancel, duration: t.duration}
	switch {
	case t.abandoned:
		out.failure = fmt.Sprintf("reference did not respond within %s; connection closed", timeout)
		c.ref.lose(out.failure, false)
	case t.panicked != nil:
		out.failure = fmt.Sprintf("reference connection lost: panic: %v", t.panicked)
		c.ref.lose(out.failure, true)
	case t.err != nil && (errors.Is(t.err, driver.ErrBadConn) || !validConn(rc)):
		out.failure = "reference connection lost: " + t.err.Error()
		c.ref.lose(out.failure, true)
	}
	if out.failure != "" && out.cancel != nil {
		out.cancel()
		out.cancel = nil
	}
	return out
}

// mirror runs the reference part of a call whose native error is e.nerr: it
// records why the reference does not run (pair failed or lost, native bad
// connection, canceled native call, missing paired object), or runs call and
// records its outcome on e.
func mirror[T any](e *entry, ctx context.Context, missing string, keep bool, call func(context.Context, driver.Conn) (T, error)) (refOutcome[T], bool) {
	if reason := e.c.skip(ctx, e.nerr); reason != "" {
		e.rec.Unmirrored = reason
		return refOutcome[T]{}, false
	}
	if missing != "" {
		e.rec.Unmirrored = missing
		return refOutcome[T]{}, false
	}
	out := refDo(e.c, ctx, keep, call)
	e.ran, e.rerr, e.failure, e.refDur = true, out.err, out.failure, out.duration
	return out, true
}

// skip returns why the reference part of a call does not run, or "".
func (c *conn) skip(ctx context.Context, nerr error) string {
	switch {
	case c.ref.code == codeFailed:
		return "reference connection failed"
	case c.ref.code == codeLost:
		return "reference connection lost"
	case errors.Is(nerr, driver.ErrBadConn):
		return "native bad connection"
	case isCanceled(nerr) && ctx.Err() != nil:
		return "canceled"
	}
	return ""
}

func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// validConn reports a reference connection's own validity (pure check).
func validConn(rc driver.Conn) (ok bool) {
	v, is := rc.(driver.Validator)
	if !is {
		return true
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return v.IsValid()
}

func closeQuietly(rc driver.Conn) {
	defer func() { _ = recover() }()
	_ = rc.Close()
}
