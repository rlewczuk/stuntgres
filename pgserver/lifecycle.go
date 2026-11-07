package pgserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
)

type dumpRequest struct {
	ctx    context.Context
	result chan error // buffered: a canceled caller never strands the worker
}

func (s *Server) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Dump serializes an explicit publication cycle with all other dumps and final
// shutdown. Cancellation bounds admission, engine work and the caller's wait.
func (s *Server) Dump(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.stage != stageRunning || s.terminal != terminalNone {
		closed := s.stage == stageClosed || s.terminal != terminalNone
		s.mu.Unlock()
		if closed {
			return net.ErrClosed
		}
		return errors.New("server is not running")
	}
	if s.cfg.DumpDir == "" {
		s.mu.Unlock()
		return nil
	}
	request := &dumpRequest{ctx: ctx, result: make(chan error, 1)}
	s.dumps = append(s.dumps, request)
	s.mu.Unlock()
	s.signal()
	select {
	case result := <-request.result:
		return result
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		select {
		case result := <-request.result:
			return result
		default:
			return net.ErrClosed
		}
	}
}

// selectTerminal makes one graceful/abort choice and closes physical admission
// under the same core lock as newClient's WaitGroup.Add. Socket I/O is unlocked.
func (s *Server) selectTerminal(mode terminalMode) {
	s.mu.Lock()
	if s.stage != stageRunning {
		s.mu.Unlock()
		return
	}
	if mode > s.terminal && !s.stoppedSelected {
		s.terminal = mode
	}
	s.core.closeAdmission()
	cancel := s.activeCancel
	abort := s.terminal == terminalAbort
	core := s.core
	s.mu.Unlock()
	if abort && cancel != nil {
		cancel()
	}
	core.stop()
	s.signal()
}

// Shutdown cancels all sessions before any rollback, joins owned work and
// publishes a final dump. A deadline bounds this wait, not server cleanup.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	stage := s.stage
	s.mu.Unlock()
	if stage != stageRunning && stage != stageClosed {
		return errors.New("server is not running")
	}
	s.selectTerminal(terminalGraceful)
	select {
	case <-s.done:
		return s.Err()
	default:
	}
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close aborts persistence, cancels transports and joins owned cleanup. Caller
// writers must eventually return; arbitrary blocked writer calls cannot be killed.
func (s *Server) Close() error {
	s.mu.Lock()
	switch s.stage {
	case stageClosed:
		result := s.result
		s.mu.Unlock()
		return result
	case stageRunning:
		s.mu.Unlock()
		s.selectTerminal(terminalAbort)
		<-s.done
		return s.Err()
	case stagePreparing, stageStarting:
		s.mu.Unlock()
		return errors.New("startup stages must be called sequentially")
	default:
		s.stage = stageClosed
		s.cfg.Accounts = nil
		s.mu.Unlock()
		result := s.logs.Close()
		s.mu.Lock()
		s.result = result
		close(s.done)
		s.mu.Unlock()
		return result
	}
}

func (s *Server) lifecycle() {
	for {
		s.mu.Lock()
		if s.terminal != terminalNone {
			pending := s.dumps
			s.dumps = nil
			s.mu.Unlock()
			for _, request := range pending {
				request.result <- net.ErrClosed
			}
			s.finalize()
			return
		}
		if len(s.dumps) == 0 {
			s.mu.Unlock()
			<-s.wake
			continue
		}
		request := s.dumps[0]
		s.dumps[0] = nil
		s.dumps = s.dumps[1:]
		ctx, cancel := context.WithCancel(request.ctx)
		s.activeCancel = cancel
		s.mu.Unlock()
		err := ctx.Err()
		if err == nil {
			err = s.dumpCycle(ctx, true)
		}
		cancel()
		s.mu.Lock()
		s.activeCancel = nil
		s.mu.Unlock()
		request.result <- err
	}
}

func (s *Server) dumpCycle(ctx context.Context, explicit bool) error {
	err := dumpDatabases(ctx, s.databases, s.cfg.DumpDir, s.logs)
	if explicit && err != nil {
		if logErr := s.logs.operational(context.Background(), slog.LevelError, "dump cycle failed", "error", err.Error()); logErr != nil {
			s.core.fail(logErr)
		}
	}
	if logErr := s.logs.failureError(); logErr != nil {
		s.core.fail(logErr)
	}
	return err
}

func (s *Server) finalize() {
	// selectTerminal already fenced Add and stopped transports. Joining accept
	// before workers also covers a socket accepted immediately before shutdown.
	s.core.stop()
	<-s.acceptDone
	s.core.workers.Wait()
	close(s.stopWatch)
	<-s.watchDone
	var final error
	s.mu.Lock()
	graceful := s.terminal == terminalGraceful
	if graceful && s.cfg.DumpDir != "" {
		ctx, cancel := context.WithCancel(context.Background())
		s.activeCancel = cancel
		s.mu.Unlock()
		final = s.dumpCycle(ctx, false)
		cancel()
		s.mu.Lock()
		s.activeCancel = nil
		// Intentional abort cancellation is not itself a server failure.
		if s.terminal == terminalAbort {
			final = withoutAbortCancellation(final)
		}
	}
	graceful = s.terminal == terminalGraceful
	if graceful {
		s.stoppedSelected = true
	}
	s.mu.Unlock()
	if graceful {
		final = errors.Join(final, s.logs.operational(context.Background(), slog.LevelInfo, "server stopped"))
	}
	closeErr := s.logs.Close()
	result := joinServerError(final, s.core.runtimeError())
	result = joinServerError(result, s.logs.failureError())
	result = joinServerError(result, closeErr)
	s.mu.Lock()
	s.result = result
	s.stage = stageClosed
	close(s.done)
	s.mu.Unlock()
}

func joinServerError(result, next error) error {
	if next == nil || errors.Is(result, next) {
		return result
	}
	return errors.Join(result, next)
}

// Remove only the intentional cancellation leaf, not joined filesystem/log
// failures that happened while an abort was canceling a final publication.
func withoutAbortCancellation(err error) error {
	if err == context.Canceled {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result error
		for _, child := range joined.Unwrap() {
			result = joinServerError(result, withoutAbortCancellation(child))
		}
		return result
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && errors.Is(err, context.Canceled) {
		return withoutAbortCancellation(wrapped.Unwrap())
	}
	return err
}
