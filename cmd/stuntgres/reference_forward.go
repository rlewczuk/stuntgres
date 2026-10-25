package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"stuntgres/internal/sqlstate"
)

// referenceForwarder mirrors one client connection onto its reference
// PostgreSQL session. Every frontend message the dispatcher accepts after
// admission is forwarded in the same order; the reference response of each
// command cycle (through its ReadyForQuery) is collected for query.log.
//
// Ownership: the serial dispatcher owns the write side (forward, await,
// takeCycle, takeIdle, close) and the fields marked "dispatcher". One reader
// goroutine decodes reference messages. mu guards the fields they share; it
// is a leaf lock: no socket I/O, logging or other lock is taken while it is
// held. config, timeout, session, warn and the session's pid/key are
// immutable after startReference, so cancelRequest may read them from any
// goroutine.
type referenceForwarder struct {
	config     *referenceConfig
	timeout    time.Duration
	session    *referenceSession // nil when the login failed
	warn       func(message string, args ...any)
	readerDone chan struct{} // nil when the login failed
	changed    chan struct{} // buffered 1; pulsed after each shared change
	closeOnce  sync.Once

	simple    bool           // dispatcher: the current cycle contains a simple Query
	result    []queryMessage // dispatcher: the awaited cycle's reference response
	resultSet bool           // dispatcher

	mu         sync.Mutex
	open       bool // a forwarded cycle awaits its reference ReadyForQuery
	batch      []queryMessage
	completed  [][]queryMessage
	idle       []queryMessage // received while no cycle was open
	copyStarts int            // CopyInResponse received in the open cycle
	copyEnds   int            // CopyDone/CopyFail sent in the open cycle
	failure    *sqlstate.Error
	closed     bool // close began: a later read error is not a failure
}

// startReference logs in to the reference. A failed login still returns a
// forwarder, already failed, so every cycle records the failure; the error
// is also returned for the operational log.
func startReference(ctx context.Context, c *referenceConfig, timeout time.Duration, startup referenceStartup, warn func(string, ...any)) (*referenceForwarder, error) {
	f := &referenceForwarder{config: c, timeout: timeout, warn: warn, changed: make(chan struct{}, 1)}
	session, err := dialReference(ctx, c, timeout, startup)
	if err != nil {
		f.failure = referenceFailure("08001", "reference connection failed: "+err.Error())
		return f, err
	}
	f.session, f.readerDone = session, make(chan struct{})
	go f.read()
	return f, nil
}

func referenceFailure(code, message string) *sqlstate.Error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

// flushesReference reports whether a forwarded message ends a burst the
// client waits on; COPY data and extended messages stay buffered until then.
func flushesReference(tag byte) bool {
	switch tag {
	case 'Q', 'S', 'H', 'F', 'c', 'f', 'X':
		return true
	}
	return false
}

// forward sends one frontend message, opening a cycle if none is open. The
// body may be a reused reader buffer; it is written before returning.
func (f *referenceForwarder) forward(m frontendMessage) {
	f.mu.Lock()
	if f.failure != nil || f.closed {
		f.mu.Unlock()
		return
	}
	if !f.open {
		f.open, f.copyStarts, f.copyEnds, f.simple = true, 0, 0, false
	}
	if m.tag == 'c' || m.tag == 'f' {
		f.copyEnds++
	}
	f.mu.Unlock()
	if m.tag == 'Q' {
		f.simple = true
	}
	f.write(m.tag, m.body, flushesReference(m.tag))
}

func (f *referenceForwarder) write(tag byte, body []byte, flush bool) {
	s := f.session
	err := s.conn.SetWriteDeadline(time.Now().Add(f.timeout))
	if err == nil {
		err = writeFrame(s.writer, tag, body)
	}
	if err == nil && flush {
		err = s.writer.Flush()
	}
	if err != nil {
		f.fail(referenceFailure("08006", "reference connection lost: "+err.Error()))
	}
}

func (f *referenceForwarder) read() {
	defer close(f.readerDone)
	var buffer []byte
	var formats []int16
	for {
		tag, body, err := readBackendMessage(f.session.reader, buffer)
		if err != nil {
			f.fail(referenceFailure("08006", "reference connection lost: "+err.Error()))
			return
		}
		buffer = body
		name, decoded := decodeBackend(tag, body, &formats)
		m := queryMessage{Type: string(tag), Name: name, Data: base64.StdEncoding.EncodeToString(body), Decoded: decoded}
		f.mu.Lock()
		if !f.open {
			f.idle = append(f.idle, m)
		} else {
			f.batch = append(f.batch, m)
			switch tag {
			case 'G':
				f.copyStarts++
			case 'Z':
				f.completed = append(f.completed, f.batch)
				f.batch, f.open = nil, false
			}
		}
		f.mu.Unlock()
		f.pulse()
	}
}

func (f *referenceForwarder) pulse() {
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// fail records the first failure and closes the reference, so no late
// response can be attributed to a later cycle.
func (f *referenceForwarder) fail(d *sqlstate.Error) {
	f.mu.Lock()
	first := f.failure == nil && !f.closed
	if f.failure == nil {
		f.failure = d
	}
	f.mu.Unlock()
	f.closeSocket()
	f.pulse()
	if first {
		f.warn("reference connection failed", "reference", f.config.display(), "error", d.Message)
	}
}

func (f *referenceForwarder) closeSocket() {
	if f.session != nil {
		f.closeOnce.Do(func() { _ = f.session.conn.Close() })
	}
}

// await waits for the reference ReadyForQuery that ends the current cycle,
// so the client never sees stuntgres's ReadyForQuery first. A reference left
// in COPY FROM STDIN that the client will never feed gets a CopyFail (and,
// for extended cycles, the Sync it ignored while copying). Past the timeout a
// CancelRequest is sent; one more timeout closes the reference.
func (f *referenceForwarder) await(ctx context.Context) {
	timer := time.NewTimer(f.timeout)
	defer timer.Stop()
	canceled := false
	for {
		f.mu.Lock()
		if len(f.completed) > 0 {
			f.result, f.resultSet = f.completed[0], true
			f.completed = f.completed[1:]
			f.mu.Unlock()
			return
		}
		if f.failure != nil || !f.open {
			f.result, f.resultSet = f.partialLocked(), true
			f.mu.Unlock()
			return
		}
		stuck := f.copyStarts > f.copyEnds
		if stuck {
			f.copyEnds++
		}
		f.mu.Unlock()
		if stuck {
			f.write('f', appendString(nil, "stuntgres did not enter COPY FROM STDIN"), f.simple)
			if !f.simple {
				f.write('S', nil, true)
			}
			continue
		}
		select {
		case <-f.changed:
		case <-ctx.Done():
			return
		case <-timer.C:
			if !canceled {
				canceled = true
				f.warn("reference response timed out; sending cancel request", "reference", f.config.display(), "timeout", f.timeout.String())
				f.cancelRequest()
				timer.Reset(f.timeout)
				continue
			}
			f.fail(referenceFailure("08006", fmt.Sprintf("reference did not respond within %s after a cancel request; connection closed", f.timeout)))
		}
	}
}

// partialLocked takes the open cycle's messages, ending with the failure if
// the reference has failed. Requires mu.
func (f *referenceForwarder) partialLocked() []queryMessage {
	out := f.batch
	f.batch, f.open = nil, false
	if f.failure != nil {
		body := diagnosticBody(*f.failure, "FATAL")
		out = append(out, queryMessage{Type: "E", Name: "ErrorResponse", Data: base64.StdEncoding.EncodeToString(body), Decoded: errorFields(body)})
	}
	return out
}

// takeCycle returns the reference response for the cycle just ended: the
// awaited one, or whatever arrived before a cycle was cut off.
func (f *referenceForwarder) takeCycle() []queryMessage {
	if f.resultSet {
		out := f.result
		f.result, f.resultSet = nil, false
		return out
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.partialLocked()
}

// takeIdle returns reference output received outside any cycle.
func (f *referenceForwarder) takeIdle() []queryMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.idle
	f.idle = nil
	return out
}

func (f *referenceForwarder) hasIdle() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.idle) != 0
}

// cancelRequest asks the reference to cancel its current command.
func (f *referenceForwarder) cancelRequest() {
	if f.session == nil {
		return
	}
	if err := sendReferenceCancel(f.config, f.timeout, f.session.pid, f.session.key); err != nil {
		f.warn("reference cancel request failed", "reference", f.config.display(), "error", err.Error())
	}
}

// close ends the reference session and joins its reader. The dispatcher has
// already forwarded Terminate when the client sent one.
func (f *referenceForwarder) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.closeSocket()
	if f.readerDone != nil {
		<-f.readerDone
	}
}
