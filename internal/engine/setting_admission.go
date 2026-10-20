package engine

import (
	"context"
	"errors"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

var statementTimeoutCause = errors.New("stuntgres: statement timeout")

// AcquireCommand captures prior settings before parsing or executing a SET.
// Initialization and native controls deliberately continue to use Acquire.
func (s *Session) AcquireCommand(ctx context.Context) (*Operation, parser.Options, error) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	op, err := s.AcquireActivity(ctx)
	if err != nil {
		return nil, parser.Options{}, err
	}
	s.settings.mu.Lock()
	standard := s.settings.typedValue("standard_conforming_strings").Boolean
	op.nonstandardStrings = !standard
	timeout := s.settings.typedValue("statement_timeout").Number
	op.lockTimeout = time.Duration(s.settings.typedValue("lock_timeout").Number) * time.Millisecond
	op.parallelWorkers = int(s.settings.typedValue("max_parallel_workers_per_gather").Number)
	transactionTimeout := s.settings.typedValue("transaction_timeout").Number
	op.noticeThreshold, _ = messageLevel(s.settings.value(settings.ClientMinMessages.Name))
	op.commandAdmission = true
	op.commandStart = start
	s.settings.mu.Unlock()
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		timed, cancel := context.WithDeadlineCause(op.ctx, deadline, statementTimeoutCause)
		s.mu.Lock()
		op.statementDeadline, op.ctx, op.timerCancel = deadline, timed, cancel
		s.mu.Unlock()
	}
	return op, parser.Options{StandardConformingStrings: standard, Context: op.ctx, Notice: op.parseNotice}, nil
}

// CommandError translates only cancellation from this command's own timer.
// A caller deadline or an unrelated SQL/adapter error must retain its identity.
func (op *Operation) CommandError(err error) error {
	var source lock.SourceError
	if errors.As(err, &source) {
		return source.Unwrap()
	}
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && errors.Is(context.Cause(op.Context()), statementTimeoutCause) {
		return sqlError("57014", "canceling statement due to statement timeout")
	}
	return op.fatalError(err)
}

func (op *Operation) refreshStatementBudget() error {
	if err := op.commandContext.Err(); err != nil {
		return err
	}
	store := op.session.settings
	store.mu.Lock()
	timeout := store.typedValue("statement_timeout").Number
	transactionTimeout := store.typedValue("transaction_timeout").Number
	lockTimeout := store.typedValue("lock_timeout").Number
	threshold, _ := messageLevel(store.value(settings.ClientMinMessages.Name))
	store.mu.Unlock()
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if op.timerCancel != nil {
		op.timerCancel()
		op.timerCancel = nil
	}
	op.ctx = op.commandContext
	op.statementDeadline = time.Time{}
	op.lockTimeout = time.Duration(lockTimeout) * time.Millisecond
	op.noticeThreshold = threshold
	op.sqlFinished = false
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		op.ctx, op.timerCancel = context.WithDeadlineCause(op.commandContext, deadline, statementTimeoutCause)
		op.statementDeadline = deadline
	}
	return nil
}
