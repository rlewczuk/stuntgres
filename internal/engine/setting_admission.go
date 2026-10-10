package engine

import (
	"context"
	"errors"
	"time"

	"stuntgres/internal/parser"
)

var statementTimeoutCause = errors.New("stuntgres: statement timeout")

// AcquireCommand captures prior settings before parsing or executing a SET.
// Initialization and native controls deliberately continue to use Acquire.
func (s *Session) AcquireCommand(ctx context.Context) (*Operation, parser.Options, error) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	op, err := s.Acquire(ctx)
	if err != nil {
		return nil, parser.Options{}, err
	}
	s.settings.mu.Lock()
	standard := s.settings.typedValue("standard_conforming_strings").boolean
	op.nonstandardStrings = !standard
	timeout := s.settings.typedValue("statement_timeout").number
	op.lockTimeout = time.Duration(s.settings.typedValue("lock_timeout").number) * time.Millisecond
	op.noticeThreshold, _ = messageLevel(s.settings.value(clientMinMessages.Name))
	op.commandAdmission = true
	op.commandStart = start
	s.settings.mu.Unlock()
	if timeout > 0 {
		op.statementDeadline = time.Now().Add(time.Duration(timeout) * time.Millisecond)
		op.ctx, op.timerCancel = context.WithDeadlineCause(op.ctx, op.statementDeadline, statementTimeoutCause)
	}
	return op, parser.Options{StandardConformingStrings: standard, Context: op.ctx}, nil
}

// CommandError translates only cancellation from this command's own timer.
// A caller deadline or an unrelated SQL/adapter error must retain its identity.
func (op *Operation) CommandError(err error) error {
	var source relationSourceError
	if errors.As(err, &source) {
		return source.error
	}
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && errors.Is(context.Cause(op.ctx), statementTimeoutCause) {
		return sqlError("57014", "canceling statement due to statement timeout")
	}
	return err
}
