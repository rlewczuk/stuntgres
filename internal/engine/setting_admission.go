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
	s.Settings.mu.Lock()
	standard := s.Settings.typedValue("standard_conforming_strings").Boolean
	op.NonstandardStrings = !standard
	timeout := s.Settings.typedValue("statement_timeout").Number
	op.LockTimeout = time.Duration(s.Settings.typedValue("lock_timeout").Number) * time.Millisecond
	op.ParallelWorkers = int(s.Settings.typedValue("max_parallel_workers_per_gather").Number)
	transactionTimeout := s.Settings.typedValue("transaction_timeout").Number
	op.NoticeThreshold, _ = messageLevel(s.Settings.value(settings.ClientMinMessages.Name))
	op.CommandAdmission = true
	op.CommandStart = start
	s.Settings.mu.Unlock()
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		timed, cancel := context.WithDeadlineCause(op.Ctx, deadline, statementTimeoutCause)
		s.Mu.Lock()
		op.StatementDeadline, op.Ctx, op.TimerCancel = deadline, timed, cancel
		s.Mu.Unlock()
	}
	return op, parser.Options{StandardConformingStrings: standard, Context: op.Ctx, Notice: op.ParseNotice}, nil
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

func (op *Operation) RefreshStatementBudget() error {
	if err := op.CommandContext.Err(); err != nil {
		return err
	}
	store := op.Session.Settings
	store.mu.Lock()
	timeout := store.typedValue("statement_timeout").Number
	transactionTimeout := store.typedValue("transaction_timeout").Number
	lockTimeout := store.typedValue("lock_timeout").Number
	threshold, _ := messageLevel(store.value(settings.ClientMinMessages.Name))
	store.mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	if op.TimerCancel != nil {
		op.TimerCancel()
		op.TimerCancel = nil
	}
	op.Ctx = op.CommandContext
	op.StatementDeadline = time.Time{}
	op.LockTimeout = time.Duration(lockTimeout) * time.Millisecond
	op.NoticeThreshold = threshold
	op.SQLFinished = false
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		op.Ctx, op.TimerCancel = context.WithDeadlineCause(op.CommandContext, deadline, statementTimeoutCause)
		op.StatementDeadline = deadline
	}
	return nil
}
