package exec

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// Exact private control value: only an admitted append consumes it. It never
// represents a provider error, EOF, or a terminal Stage failure.
var errForeignPending = errors.New("stuntgres: foreign request pending")

type foreignAsyncState struct {
	capable       bool
	request       catalog.ForeignAsyncRequest
	ready         <-chan struct{}
	values        []sqltype.Value
	buffered, eof bool
	start         func(context.Context) error
	importRow     func(context.Context, catalog.ForeignRow) ([]sqltype.Value, error)
}

func (e *ForeignExecution) requestAsync(ctx context.Context) error {
	a := &e.async
	if a.request != nil || a.buffered || a.eof {
		return nil
	}
	provider, ok := e.Scan.(catalog.ForeignAsyncScan)
	if !ok {
		return fmt.Errorf("stuntgres: async-capable provider returned a scan without async requests")
	}
	request, err := provider.RequestForeignScan(ctx)
	a.request = request // nonnil+error transfers ownership before inspecting error
	if err != nil {
		return ForeignCallbackError(err)
	}
	if request == nil {
		return fmt.Errorf("stuntgres: async provider returned a nil request")
	}
	a.ready = request.Ready()
	if a.ready == nil {
		return fmt.Errorf("stuntgres: async provider returned a nil Ready channel")
	}
	return nil
}

func (e *ForeignExecution) cancelAsync() {
	if e.async.request != nil {
		e.async.request.Cancel()
	}
}
func (e *ForeignExecution) closeAsync(ctx context.Context) error {
	a := &e.async
	if a.request == nil {
		return nil
	}
	request := a.request
	a.request, a.ready = nil, nil
	return ForeignCallbackError(request.Close(context.WithoutCancel(ctx)))
}

// settleAsync imports while the provider still owns stable result storage. It
// neither evaluates local expressions nor advances a second remote request.
func (e *ForeignExecution) settleAsync(ctx context.Context, wait bool) error {
	a := &e.async
	if a.request == nil {
		return nil
	}
	if wait {
		select {
		case <-a.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		select {
		case <-a.ready:
		default:
			return errForeignPending
		}
	}
	row, present, err := a.request.Complete(ctx)
	err = ForeignCallbackError(err)
	if err == nil && present {
		a.values, err = a.importRow(ctx, row)
	}
	closeErr := e.closeAsync(ctx)
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	a.buffered, a.eof = present, !present
	return nil
}

// Quiesce is the successful finite FETCH/MOVE boundary. Keep one owned source
// row, never a request, worker, or prior motion context, for the next demand.
func (owner *ForeignScanOwner) Quiesce(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	for _, source := range owner.sources {
		for _, execution := range source.Executions {
			if err := execution.settleAsync(ctx, true); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// Terminal cleanup is three sweeps: signal every request, join every request,
// then the existing owner may End scans. In particular End cannot be the first
// cancellation signal, and no slow join delays cancellation of its siblings.
func (owner *ForeignScanOwner) finishRequests(ctx context.Context) error {
	for _, source := range owner.sources {
		for _, execution := range source.Executions {
			execution.cancelAsync()
		}
	}
	var failure error
	for _, source := range owner.sources {
		for _, execution := range source.Executions {
			if err := execution.closeAsync(ctx); failure == nil {
				failure = err
			}
			execution.async.values = nil
			execution.async.buffered = false
		}
	}
	return failure
}

// CaptureAsyncAdmission snapshots the current admitted host once. A retained
// cursor calls this at DECLARE; subsequent FETCH hosts cannot change eligibility.
func (s *State) CaptureAsyncAdmission() {
	if s.asyncCaptured {
		return
	}
	s.asyncCaptured = true
	if s.Source != nil {
		if host := s.Source.LiveHost(); host != nil {
			s.AsyncAppend = host.AsyncAppendEnabled()
		}
	}
}

// Invocation lifetimes can short-circuit and reopen their captured sources.
// They borrow the ordinary reusable frontier rather than admitting new async
// executions. The sticky fact survives child constructors that omit frames.
func (s *State) asyncAppendEligible() bool {
	return s.AsyncAppend && !s.asyncInvocation && s.Outer == nil && s.Invocation == nil
}

func (s *State) inheritAsyncAdmission(parent *State) {
	s.AsyncAppend, s.asyncCaptured = parent.AsyncAppend, parent.asyncCaptured
	s.asyncInvocation = s.asyncInvocation || parent.asyncInvocation ||
		parent.Outer != nil || parent.Invocation != nil || s.Outer != nil || s.Invocation != nil
	s.asyncFrontier = nil
	if s.asyncAppendEligible() {
		s.asyncFrontier = parent.asyncFrontier
	}
}
