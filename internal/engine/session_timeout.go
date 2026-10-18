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
	scope      *StatementScope
	ref        TxRef
}

type sessionTimeouts struct {
	transaction          sessionTimeout
	idle                 sessionTimeout
	transactionLimit     time.Duration
	idleTransactionLimit time.Duration
	idleSessionLimit     time.Duration
	explicit             bool
	authority            TxRef
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
	slot.scope = nil
	slot.ref = TxRef{}
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
		if slot.scope == nil && (slot.ref == (TxRef{}) || slot.ref != s.timeouts.authority) {
			return
		}
		// Released ProcessInterrupts handles already-pending statement ERROR
		// before session FATAL. ERROR recovery will keep this budget stopped.
		for op := range s.operations {
			scope := op.timeoutScope
			if scope != nil && !op.sqlFinished && (slot.scope == scope || (slot.ref != (TxRef{}) && scope.timeoutRef == slot.ref)) &&
				(op.pendingTimeout(now) || (!op.statementDeadline.IsZero() && !now.Before(op.statementDeadline)) ||
					errors.Is(context.Cause(op.ctx), statementTimeoutCause)) {
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
	s.timeouts.transactionLimit = time.Duration(settings.typedValue("transaction_timeout").number) * time.Millisecond
	s.timeouts.idleTransactionLimit = time.Duration(settings.typedValue("idle_in_transaction_session_timeout").number) * time.Millisecond
	s.timeouts.idleSessionLimit = time.Duration(settings.typedValue("idle_session_timeout").number) * time.Millisecond
	if s.timeouts.transactionLimit == 0 {
		s.timeouts.transaction.stop()
	} else if assignment && s.state == live && s.timeouts.transaction.deadline.IsZero() && s.timeouts.authority != (TxRef{}) {
		s.timeouts.transaction.ref = s.timeouts.authority
		s.armTimeoutLocked(&s.timeouts.transaction, now.Add(s.timeouts.transactionLimit), "25P04")
	}
}

func (s *Session) startProvisionalTimeout(scope *StatementScope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope.timeoutRef = s.timeouts.authority
	if scope.op != nil {
		scope.op.timeoutScope = scope
	}
	if s.state != live || s.timeouts.authority != (TxRef{}) || s.timeouts.transactionLimit == 0 {
		return
	}
	s.timeouts.transaction.scope = scope
	s.armTimeoutLocked(&s.timeouts.transaction, scope.transactionStart.Add(s.timeouts.transactionLimit), "25P04")
}

// Called under Session.mu at canonical birth; adopting the provisional slot
// neither mints a TxRef early nor restarts or allocates another runtime timer.
func (s *Session) beginTimeoutLocked(ref TxRef, explicit bool, now time.Time) {
	s.timeouts.authority, s.timeouts.explicit = ref, explicit
	slot := &s.timeouts.transaction
	if slot.scope != nil && !slot.deadline.IsZero() {
		slot.scope.timeoutRef = ref
		slot.scope, slot.ref = nil, ref
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
	s.timeouts.authority = TxRef{}
	s.timeouts.explicit = false
}
func (s *Session) retireTransactionTimeout() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireTransactionTimeoutLocked()
}
func (s *Session) finishScopeTimeout(scope *StatementScope, failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := &s.timeouts.transaction
	if scope.op != nil && scope.op.timeoutScope == scope {
		scope.op.sqlFinished = !scope.message
		scope.op.pendingTimeoutRequest.Store(nil)
		scope.op.pendingTimeoutError.Store(false)
	}
	if slot.scope == scope || (failed && slot.ref != (TxRef{}) && (slot.ref == scope.timeoutRef || slot.ref == scope.txRef)) {
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
	if op.sourceContext.Err() != nil {
		return err
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	return op.fatalErrorLocked(err)
}

// Session.mu is already held by operation/publication validation.
func (op *Operation) fatalErrorLocked(err error) error {
	if err == nil || !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) || op.sourceContext.Err() != nil {
		return err
	}
	s := op.session
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
