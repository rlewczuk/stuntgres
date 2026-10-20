package engine

import (
	"context"
	"errors"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Session.mu owns both slots and the minimal ready/authority projections below.
// Callbacks never inspect transaction, settings, catalog or lock-manager state.
// Transaction admission publishes transitions in the existing owner lock order.
type sessionTimeout struct {
	Timer      *time.Timer
	Generation uint64
	Deadline   time.Time
	Lease      *StatementLease
	Ref        ids.TxRef
}

type sessionTimeouts struct {
	Transaction          sessionTimeout
	Idle                 sessionTimeout
	TransactionLimit     time.Duration
	IdleTransactionLimit time.Duration
	IdleSessionLimit     time.Duration
	Explicit             bool
	Authority            ids.TxRef
	SQLLeases            int
	Fatal                *sqlstate.Error
}

func (slot *sessionTimeout) Stop() {
	slot.Generation++
	if slot.Timer != nil {
		slot.Timer.Stop()
		slot.Timer = nil
	}
	slot.Deadline = time.Time{}
	slot.Lease = nil
	slot.Ref = ids.TxRef{}
}

func timeoutFatal(code string) *sqlstate.Error {
	message := "terminating connection due to transaction timeout"
	switch code {
	case "25P03":
		message = "terminating connection due to idle-in-transaction timeout"
	case "57P05":
		message = "terminating connection due to idle-session timeout"
	}
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

func (s *Session) armTimeoutLocked(slot *sessionTimeout, deadline time.Time, code string) {
	slot.Generation++
	slot.Deadline = deadline
	generation, token := slot.Generation, s.SessionToken
	slot.Timer = time.AfterFunc(time.Until(deadline), func() {
		s.Mu.Lock()
		defer s.Mu.Unlock()
		s.ExpireTimeoutLocked(slot, generation, token, code, time.Now())
	})
}

// Explicit timestamps/generations make stale-callback transitions testable
// without a scheduler abstraction or timing-dependent unit tests.
func (s *Session) ExpireTimeoutLocked(slot *sessionTimeout, generation uint64, token ids.SessionToken, code string, now time.Time) {
	if s.State != live || token != s.SessionToken || slot.Generation != generation || slot.Deadline.IsZero() || now.Before(slot.Deadline) {
		return
	}
	if slot == &s.Timeouts.Transaction {
		if s.Timeouts.TransactionLimit == 0 {
			return
		}
		if slot.Lease == nil && (slot.Ref == (ids.TxRef{}) || slot.Ref != s.Timeouts.Authority) {
			return
		}
		// Released ProcessInterrupts handles already-pending statement ERROR
		// before session FATAL. ERROR recovery will keep this budget stopped.
		for op := range s.Operations {
			lease := op.Lease
			if lease != nil && !op.SQLFinished && (slot.Lease == lease || (slot.Ref != (ids.TxRef{}) && lease.TimeoutRef == slot.Ref)) &&
				(op.LockWait.Pending(now) || (!op.StatementDeadline.IsZero() && !now.Before(op.StatementDeadline)) ||
					errors.Is(context.Cause(op.Ctx), statementTimeoutCause)) {
				slot.Stop()
				return
			}
		}
		// An idle-in-transaction flag is processed before the transaction flag
		// when both are already due, not according to callback scheduling.
		idle := &s.Timeouts.Idle
		if s.Timeouts.SQLLeases == 0 && s.Timeouts.Explicit && s.Timeouts.IdleTransactionLimit > 0 &&
			!idle.Deadline.IsZero() && !now.Before(idle.Deadline) {
			code = "25P03"
		}
	} else {
		if s.Timeouts.SQLLeases != 0 {
			return
		}
		if code == "25P03" && (!s.Timeouts.Explicit || s.Timeouts.IdleTransactionLimit == 0) {
			return
		}
		if code == "57P05" && (s.Timeouts.Explicit || s.Timeouts.IdleSessionLimit == 0) {
			return
		}
	}
	s.Timeouts.Fatal = timeoutFatal(code)
	s.requestEndLocked(Terminated)
}

// Admission observes elapsed budgets before a delayed callback can be outrun
// by new work. Keep the callback's authority and pending-interrupt priorities.
// Session.mu is held, and both slots are observed at the same instant.
func (s *Session) observeTimeoutsLocked(now time.Time) {
	slot := &s.Timeouts.Transaction
	s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", now)
	code := "57P05"
	if s.Timeouts.Explicit {
		code = "25P03"
	}
	slot = &s.Timeouts.Idle
	s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, code, now)
}

func (s *Session) ReadyTimeoutLocked(now time.Time) {
	if s.State != live || s.Timeouts.SQLLeases != 0 {
		return
	}
	s.Timeouts.Idle.Stop()
	limit, code := s.Timeouts.IdleSessionLimit, "57P05"
	if s.Timeouts.Explicit {
		limit, code = s.Timeouts.IdleTransactionLimit, "25P03"
		if s.Timeouts.TransactionLimit != 0 && limit >= s.Timeouts.TransactionLimit {
			limit = 0
		}
	}
	if limit > 0 {
		s.armTimeoutLocked(&s.Timeouts.Idle, now.Add(limit), code)
	}
}

// settings.mu and Session.mu are held. Ordinary assignment and journal
// restoration are distinct: restoring a positive value must not rearm a budget
// retired by SET0 or ERROR. Explicit assignment can arm an inactive live owner.
func (s *Session) PublishTimeoutSettingsLocked(settings *sessionSettings, assignment bool, now time.Time) {
	s.Timeouts.TransactionLimit = time.Duration(settings.TypedValue("transaction_timeout").Number) * time.Millisecond
	s.Timeouts.IdleTransactionLimit = time.Duration(settings.TypedValue("idle_in_transaction_session_timeout").Number) * time.Millisecond
	s.Timeouts.IdleSessionLimit = time.Duration(settings.TypedValue("idle_session_timeout").Number) * time.Millisecond
	if s.Timeouts.TransactionLimit == 0 {
		s.Timeouts.Transaction.Stop()
	} else if assignment && s.State == live && s.Timeouts.Transaction.Deadline.IsZero() && s.Timeouts.Authority != (ids.TxRef{}) {
		s.Timeouts.Transaction.Ref = s.Timeouts.Authority
		s.armTimeoutLocked(&s.Timeouts.Transaction, now.Add(s.Timeouts.TransactionLimit), "25P04")
	}
}

// startProvisionalTimeout makes lease op's current statement and, before a
// transaction is born, arms the transaction timeout from transactionStart.
func (s *Session) StartProvisionalTimeout(lease *StatementLease, op *Operation, transactionStart time.Time) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	lease.TimeoutRef = s.Timeouts.Authority
	if op != nil {
		op.Lease = lease
	}
	if s.State != live || s.Timeouts.Authority != (ids.TxRef{}) || s.Timeouts.TransactionLimit == 0 {
		return
	}
	s.Timeouts.Transaction.Lease = lease
	s.armTimeoutLocked(&s.Timeouts.Transaction, transactionStart.Add(s.Timeouts.TransactionLimit), "25P04")
}

// Called under Session.mu at canonical birth; adopting the provisional slot
// neither mints a TxRef early nor restarts or allocates another runtime timer.
func (s *Session) BeginTimeoutLocked(ref ids.TxRef, explicit bool, now time.Time) {
	s.Timeouts.Authority, s.Timeouts.Explicit = ref, explicit
	slot := &s.Timeouts.Transaction
	if slot.Lease != nil && !slot.Deadline.IsZero() {
		slot.Lease.TimeoutRef = ref
		slot.Lease, slot.Ref = nil, ref
		return
	}
	slot.Stop()
	if s.State == live && s.Timeouts.TransactionLimit > 0 {
		slot.Ref = ref
		s.armTimeoutLocked(slot, now.Add(s.Timeouts.TransactionLimit), "25P04")
	}
}

func (s *Session) RetireTransactionTimeoutLocked() {
	s.Timeouts.Transaction.Stop()
	s.Timeouts.Authority = ids.TxRef{}
	s.Timeouts.Explicit = false
}
func (s *Session) RetireTransactionTimeout() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.RetireTransactionTimeoutLocked()
}

// finishScopeTimeout ends lease's statement timeout state. op, message and
// txRef are the finishing statement's operation, message flag and
// transaction.
func (s *Session) FinishScopeTimeout(lease *StatementLease, op *Operation, message bool, txRef ids.TxRef, failed bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	slot := &s.Timeouts.Transaction
	if op != nil && op.Lease == lease {
		op.SQLFinished = !message
		op.LockWait.Reset()
	}
	if slot.Lease == lease || (failed && slot.Ref != (ids.TxRef{}) && (slot.Ref == lease.TimeoutRef || slot.Ref == txRef)) {
		slot.Stop()
	}
}

// Check preserves the physical fatal for the first pinned API call. Pool reuse
// uses Reuse/IsLive instead and can discard before admitting new work safely.
func (s *Session) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.observeTimeoutsLocked(time.Now())
	if s.State != live {
		if s.Timeouts.Fatal != nil {
			copy := *s.Timeouts.Fatal
			return &copy
		}
		return errors.New("stuntgres: session unavailable")
	}
	return nil
}

func (op *Operation) fatalError(err error) error {
	if err == nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	if op.SourceContext.Err() != nil {
		return err
	}
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return op.FatalErrorLocked(err)
}

// Session.mu is already held by operation/publication validation.
func (op *Operation) FatalErrorLocked(err error) error {
	if err == nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) || op.SourceContext.Err() != nil {
		return err
	}
	s := op.Session
	if s.Timeouts.Fatal != nil {
		copy := *s.Timeouts.Fatal
		return &copy
	}
	return err
}

// Go controls and Ping have SQL activity/ready boundaries but do not parse SQL
// or own a statement timer. Native controls retain plain Acquire.
func (s *Session) AcquireActivity(ctx context.Context) (*Operation, error) {
	op, err := s.acquire(ctx, true)
	return op, err
}
