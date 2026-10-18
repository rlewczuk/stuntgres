package lock

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"stuntgres/internal/sqlstate"
)

const (
	relationTimeoutQueued uint32 = iota
	relationTimeoutTerminal
	relationTimeoutLock
	relationTimeoutStatement
)

func relationTimeoutSourceCause(policy Policy, lockDeadline, now time.Time) error {
	if policy.Source != nil {
		if errors.Is(policy.Source.Err(), context.Canceled) {
			return context.Canceled
		}
		if deadline, ok := policy.Source.Deadline(); ok && !now.Before(deadline) &&
			(lockDeadline.IsZero() || !lockDeadline.Before(deadline)) &&
			(policy.StatementDeadline.IsZero() || !policy.StatementDeadline.Before(deadline)) {
			return context.DeadlineExceeded
		}
	}
	return nil
}

// Share deadline classification without constructing a discarded SQL error in
// the lifecycle callback. Equal absolute SQL deadlines retain lock precedence.
func relationTimeoutWinner(policy Policy, lockDeadline, now time.Time) uint32 {
	lockExpired := !lockDeadline.IsZero() && !now.Before(lockDeadline)
	statementExpired := !policy.StatementDeadline.IsZero() && !now.Before(policy.StatementDeadline)
	if lockExpired && (!statementExpired || !policy.StatementDeadline.Before(lockDeadline)) {
		return relationTimeoutLock
	}
	if statementExpired {
		return relationTimeoutStatement
	}
	return relationTimeoutTerminal
}

func relationTimeoutCode(err error) uint32 {
	diagnostic, ok := err.(*sqlstate.Error)
	if ok && diagnostic.Severity == "ERROR" {
		switch diagnostic.Code {
		case "55P03":
			return relationTimeoutLock
		case "57014":
			return relationTimeoutStatement
		}
	}
	return relationTimeoutTerminal
}

func relationTimeoutError(state uint32) error {
	switch state {
	case relationTimeoutLock:
		return sqlError("55P03", "canceling statement due to lock timeout")
	case relationTimeoutStatement:
		return sqlError("57014", "canceling statement due to statement timeout")
	}
	return nil
}

// TimeoutSlot projects one admitted statement's queued SQL-deadline
// request to its session timeout observer without entering manager metadata.
// The request publishes itself on enqueue and retires on terminal outcome.
type TimeoutSlot struct {
	request atomic.Pointer[relationRequest] // exact queued timeout-tracked request
	failed  atomic.Bool                     // sticky: a tracked request ended with a SQL timeout ERROR
}

func (slot *TimeoutSlot) Reset() {
	slot.request.Store(nil)
	slot.failed.Store(false)
}

func (r *relationRequest) tracksTimeout() bool {
	return r.policy.TimeoutSlot != nil && (!r.deadline.IsZero() || !r.policy.StatementDeadline.IsZero())
}

// The manager owns queue/grant/retirement. This atomic claim only prevents a
// grant from racing Session's observation of an already-due SQL ERROR.
func (r *relationRequest) claimTimeoutTerminal(err error) error {
	if !r.tracksTimeout() {
		return err
	}
	state := relationTimeoutCode(err)
	if r.timeoutState.CompareAndSwap(relationTimeoutQueued, state) {
		return err
	}
	state = r.timeoutState.Load()
	if state == relationTimeoutLock || state == relationTimeoutStatement {
		if relationTimeoutCode(err) == state {
			return err
		}
		return relationTimeoutError(state)
	}
	return err
}

func (r *relationRequest) timeoutWaitError(now time.Time) error {
	if r.tracksTimeout() {
		if claimed := relationTimeoutError(r.timeoutState.Load()); claimed != nil {
			return claimed
		}
	}
	return WaitError(r.ctx, r.policy, r.deadline, now)
}

func (r *relationRequest) finishTimeout(err error) error {
	err = r.claimTimeoutTerminal(err)
	if r.tracksTimeout() {
		slot := r.policy.TimeoutSlot
		if state := r.timeoutState.Load(); state == relationTimeoutLock || state == relationTimeoutStatement {
			slot.failed.Store(true)
		}
		slot.request.CompareAndSwap(r, nil)
	}
	return err
}

// Session.mu is held. Read no manager-owned fields; the immutable deadlines
// and atomic state are published with the exact queued request pointer.
func (slot *TimeoutSlot) Pending(now time.Time) bool {
	if slot.failed.Load() {
		return true
	}
	if r := slot.request.Load(); r != nil {
		for {
			state := r.timeoutState.Load()
			if state == relationTimeoutLock || state == relationTimeoutStatement {
				return true
			}
			if state != relationTimeoutQueued {
				break
			}
			// Preserve caller cancellation and the existing absolute SQL winner.
			if relationTimeoutSourceCause(r.policy, r.deadline, now) != nil {
				break
			}
			state = relationTimeoutWinner(r.policy, r.deadline, now)
			if state == relationTimeoutTerminal {
				break
			}
			if r.timeoutState.CompareAndSwap(relationTimeoutQueued, state) {
				return true
			}
		}
	}
	// Terminal publication stores the sticky ERROR before clearing the pointer.
	// Recheck after a concurrent retirement, rather than losing that transition.
	return slot.failed.Load()
}
