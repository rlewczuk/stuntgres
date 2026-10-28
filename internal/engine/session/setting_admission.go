package session

import (
	"context"
	"errors"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

var StatementTimeoutCause = errors.New("stuntgres: statement timeout")

// AcquireCommand captures prior settings before parsing or executing a SET.
// Initialization and native controls deliberately continue to use Acquire.
func (s *Session) AcquireCommand(ctx context.Context) (*Operation, parser.Options, error) {
	start := time.Now().UTC().Truncate(time.Microsecond)
	op, err := s.AcquireActivity(ctx)
	if err != nil {
		return nil, parser.Options{}, err
	}
	s.Settings.Mu.Lock()
	standard := s.Settings.TypedValue("standard_conforming_strings").Boolean
	op.NonstandardStrings = !standard
	timeout := s.Settings.TypedValue("statement_timeout").Number
	op.LockTimeout = time.Duration(s.Settings.TypedValue("lock_timeout").Number) * time.Millisecond
	op.ParallelWorkers = int(s.Settings.TypedValue("max_parallel_workers_per_gather").Number)
	op.PlannerCosts = plan.PlannerCosts{
		Tuple:                  s.Settings.TypedValue("cpu_tuple_cost").Number,
		IndexTuple:             s.Settings.TypedValue("cpu_index_tuple_cost").Number,
		Operator:               s.Settings.TypedValue("cpu_operator_cost").Number,
		ParallelSetup:          s.Settings.TypedValue("parallel_setup_cost").Number,
		ParallelTuple:          s.Settings.TypedValue("parallel_tuple_cost").Number,
		MinParallelTableBlocks: s.Settings.TypedValue("min_parallel_table_scan_size").Number,
	}
	op.StatisticsTarget = int(s.Settings.TypedValue("default_statistics_target").Number)
	op.ConstraintExclusion = s.Settings.Value("constraint_exclusion")
	op.TableAccessMethod = s.Settings.Value("default_table_access_method")
	op.CursorTupleFraction = s.Settings.TypedValue("cursor_tuple_fraction").Number
	op.RecursiveWorktableFactor = s.Settings.TypedValue("recursive_worktable_factor").Number
	op.AsyncAppend = s.Settings.TypedValue("enable_async_append").Boolean
	op.PlannerMethods = s.Settings.PlannerMethodsLocked()
	transactionTimeout := s.Settings.TypedValue("transaction_timeout").Number
	op.NoticeThreshold, _ = messageLevel(s.Settings.Value(settings.ClientMinMessages.Name))
	op.CommandAdmission = true
	op.CommandStart = start
	s.Settings.Mu.Unlock()
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		timed, cancel := context.WithDeadlineCause(op.Ctx, deadline, StatementTimeoutCause)
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
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && errors.Is(context.Cause(op.Context()), StatementTimeoutCause) {
		return sqlError("57014", "canceling statement due to statement timeout")
	}
	return op.fatalError(err)
}

// StatementStart is the command start captured at admission, or the current
// time at microsecond precision for an operation without one.
func (op *Operation) StatementStart() time.Time {
	if op != nil && !op.CommandStart.IsZero() {
		return op.CommandStart
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

func (op *Operation) RefreshStatementBudget() error {
	if err := op.CommandContext.Err(); err != nil {
		return err
	}
	store := op.EffectiveSettings()
	store.Mu.Lock()
	timeout := store.TypedValue("statement_timeout").Number
	transactionTimeout := store.TypedValue("transaction_timeout").Number
	lockTimeout := store.TypedValue("lock_timeout").Number
	threshold, _ := messageLevel(store.Value(settings.ClientMinMessages.Name))
	store.Mu.Unlock()
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
		op.Ctx, op.TimerCancel = context.WithDeadlineCause(op.CommandContext, deadline, StatementTimeoutCause)
		op.StatementDeadline = deadline
	}
	return nil
}
