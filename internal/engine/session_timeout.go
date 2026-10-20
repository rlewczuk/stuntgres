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
	timer      *time.Timer
	generation uint64
	deadline   time.Time
	lease      *StatementLease
	ref        ids.TxRef
}

type sessionTimeouts struct {
	transaction          sessionTimeout
	idle                 sessionTimeout
	transactionLimit     time.Duration
	idleTransactionLimit time.Duration
	idleSessionLimit     time.Duration
	explicit             bool
	authority            ids.TxRef
	sqlLeases            int
	fatal                *sqlstate.Error
}

func (slot *sessionTimeout) stop() {
	slot.generation++
	if slot.timer != nil {
		slot.timer.Stop()
		slot.timer = nil
	}
	slot.deadline = time.Time{}
	slot.lease = nil
	slot.ref = ids.TxRef{}
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
	slot.generation++
	slot.deadline = deadline
	generation, token := slot.generation, s.token
	slot.timer = time.AfterFunc(time.Until(deadline), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.expireTimeoutLocked(slot, generation, token, code, time.Now())
	})
}

// Explicit timestamps/generations make stale-callback transitions testable
// without a scheduler abstraction or timing-dependent unit tests.
func (s *Session) expireTimeoutLocked(slot *sessionTimeout, generation uint64, token ids.SessionToken, code string, now time.Time) {
	if s.state != live || token != s.token || slot.generation != generation || slot.deadline.IsZero() || now.Before(slot.deadline) {
		return
	}
	if slot == &s.timeouts.transaction {
		if s.timeouts.transactionLimit == 0 {
			return
		}
		if slot.lease == nil && (slot.ref == (ids.TxRef{}) || slot.ref != s.timeouts.authority) {
			return
		}
		// Released ProcessInterrupts handles already-pending statement ERROR
		// before session FATAL. ERROR recovery will keep this budget stopped.
		for op := range s.operations {
			lease := op.Lease
			if lease != nil && !op.SQLFinished && (slot.lease == lease || (slot.ref != (ids.TxRef{}) && lease.TimeoutRef == slot.ref)) &&
				(op.LockWait.Pending(now) || (!op.StatementDeadline.IsZero() && !now.Before(op.StatementDeadline)) ||
					errors.Is(context.Cause(op.Ctx), statementTimeoutCause)) {
				slot.stop()
				return
			}
		}
		// An idle-in-transaction flag is processed before the transaction flag
		// when both are already due, not according to callback scheduling.
		idle := &s.timeouts.idle
		if s.timeouts.sqlLeases == 0 && s.timeouts.explicit && s.timeouts.idleTransactionLimit > 0 &&
			!idle.deadline.IsZero() && !now.Before(idle.deadline) {
			code = "25P03"
		}
	} else {
		if s.timeouts.sqlLeases != 0 {
			return
		}
		if code == "25P03" && (!s.timeouts.explicit || s.timeouts.idleTransactionLimit == 0) {
			return
		}
		if code == "57P05" && (s.timeouts.explicit || s.timeouts.idleSessionLimit == 0) {
			return
		}
	}
	s.timeouts.fatal = timeoutFatal(code)
	s.requestEndLocked(Terminated)
}

// Admission observes elapsed budgets before a delayed callback can be outrun
// by new work. Keep the callback's authority and pending-interrupt priorities.
// Session.mu is held, and both slots are observed at the same instant.
func (s *Session) observeTimeoutsLocked(now time.Time) {
	slot := &s.timeouts.transaction
	s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", now)
	code := "57P05"
	if s.timeouts.explicit {
		code = "25P03"
	}
	slot = &s.timeouts.idle
	s.expireTimeoutLocked(slot, slot.generation, s.token, code, now)
}

func (s *Session) readyTimeoutLocked(now time.Time) {
	if s.state != live || s.timeouts.sqlLeases != 0 {
		return
	}
	s.timeouts.idle.stop()
	limit, code := s.timeouts.idleSessionLimit, "57P05"
	if s.timeouts.explicit {
		limit, code = s.timeouts.idleTransactionLimit, "25P03"
		if s.timeouts.transactionLimit != 0 && limit >= s.timeouts.transactionLimit {
			limit = 0
		}
	}
	if limit > 0 {
		s.armTimeoutLocked(&s.timeouts.idle, now.Add(limit), code)
	}
}

// settings.mu and Session.mu are held. Ordinary assignment and journal
// restoration are distinct: restoring a positive value must not rearm a budget
// retired by SET0 or ERROR. Explicit assignment can arm an inactive live owner.
func (s *Session) publishTimeoutSettingsLocked(settings *sessionSettings, assignment bool, now time.Time) {
	s.timeouts.transactionLimit = time.Duration(settings.typedValue("transaction_timeout").Number) * time.Millisecond
	s.timeouts.idleTransactionLimit = time.Duration(settings.typedValue("idle_in_transaction_session_timeout").Number) * time.Millisecond
	s.timeouts.idleSessionLimit = time.Duration(settings.typedValue("idle_session_timeout").Number) * time.Millisecond
	if s.timeouts.transactionLimit == 0 {
		s.timeouts.transaction.stop()
	} else if assignment && s.state == live && s.timeouts.transaction.deadline.IsZero() && s.timeouts.authority != (ids.TxRef{}) {
		s.timeouts.transaction.ref = s.timeouts.authority
		s.armTimeoutLocked(&s.timeouts.transaction, now.Add(s.timeouts.transactionLimit), "25P04")
	}
}

// startProvisionalTimeout makes lease op's current statement and, before a
// transaction is born, arms the transaction timeout from transactionStart.
func (s *Session) startProvisionalTimeout(lease *StatementLease, op *Operation, transactionStart time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease.TimeoutRef = s.timeouts.authority
	if op != nil {
		op.Lease = lease
	}
	if s.state != live || s.timeouts.authority != (ids.TxRef{}) || s.timeouts.transactionLimit == 0 {
		return
	}
	s.timeouts.transaction.lease = lease
	s.armTimeoutLocked(&s.timeouts.transaction, transactionStart.Add(s.timeouts.transactionLimit), "25P04")
}

// Called under Session.mu at canonical birth; adopting the provisional slot
// neither mints a TxRef early nor restarts or allocates another runtime timer.
func (s *Session) beginTimeoutLocked(ref ids.TxRef, explicit bool, now time.Time) {
	s.timeouts.authority, s.timeouts.explicit = ref, explicit
	slot := &s.timeouts.transaction
	if slot.lease != nil && !slot.deadline.IsZero() {
		slot.lease.TimeoutRef = ref
		slot.lease, slot.ref = nil, ref
		return
	}
	slot.stop()
	if s.state == live && s.timeouts.transactionLimit > 0 {
		slot.ref = ref
		s.armTimeoutLocked(slot, now.Add(s.timeouts.transactionLimit), "25P04")
	}
}

func (s *Session) retireTransactionTimeoutLocked() {
	s.timeouts.transaction.stop()
	s.timeouts.authority = ids.TxRef{}
	s.timeouts.explicit = false
}
func (s *Session) retireTransactionTimeout() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireTransactionTimeoutLocked()
}

// finishScopeTimeout ends lease's statement timeout state. op, message and
// txRef are the finishing statement's operation, message flag and
// transaction.
func (s *Session) finishScopeTimeout(lease *StatementLease, op *Operation, message bool, txRef ids.TxRef, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := &s.timeouts.transaction
	if op != nil && op.Lease == lease {
		op.SQLFinished = !message
		op.LockWait.Reset()
	}
	if slot.lease == lease || (failed && slot.ref != (ids.TxRef{}) && (slot.ref == lease.TimeoutRef || slot.ref == txRef)) {
		slot.stop()
	}
}

// Check preserves the physical fatal for the first pinned API call. Pool reuse
// uses Reuse/IsLive instead and can discard before admitting new work safely.
func (s *Session) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observeTimeoutsLocked(time.Now())
	if s.state != live {
		if s.timeouts.fatal != nil {
			copy := *s.timeouts.fatal
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return op.FatalErrorLocked(err)
}

// Session.mu is already held by operation/publication validation.
func (op *Operation) FatalErrorLocked(err error) error {
	if err == nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) || op.SourceContext.Err() != nil {
		return err
	}
	s := op.Session
	if s.timeouts.fatal != nil {
		copy := *s.timeouts.fatal
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
