package session

import (
	"context"
	"time"

	"stuntgres/internal/sqlstate"
)

// UserCancelCause is the sole immutable identity for a requested query cancel.
// Execution owners enrich copies, never this shared cross-session value.
var UserCancelCause = &sqlstate.Error{
	Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
	Message: "canceling statement due to user request",
}

// SignalTimeout matches the released int64 argument's assignment to signed C int
// before negative validation. It is independent of the embedding host's int size.
func SignalTimeout(input int64) int64 { return int64(int32(input)) }

// Caller holds Mu. Command admission and completion use this same mutex; controls
// and background activity never qualify, and idle requests leave no pending bit.
func (s *Session) CancelQueryLocked() {
	if s.State != Live {
		return
	}
	for op := range s.Operations {
		if op.CommandAdmission && !op.Released && !op.SQLFinished {
			op.Cancel(UserCancelCause)
		}
	}
}

// Caller holds Mu. Physical fatal authority is separate from context first-cause
// semantics: an earlier user cancel must not conceal subsequent termination.
func (s *Session) TerminateLocked() {
	if s.State == Ended {
		return
	}
	if s.Timeouts.Fatal == nil {
		s.Timeouts.Fatal = &sqlstate.Error{
			Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01",
			Message: "terminating connection due to administrator command",
		}
	}
	s.requestEndLocked(Terminated)
}

// WaitTermination observes real cleanup completion, not request delivery or the
// cleanup reporter's error. No lifecycle/registry/engine lock is held here.
// milliseconds is the already normalized positive SQL timeout.
func (s *Session) WaitTermination(ctx context.Context, milliseconds int64) (bool, error) {
	select {
	case <-s.Completed:
		return true, nil
	default:
	}
	timer := time.NewTimer(time.Duration(milliseconds) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-s.Completed:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		select {
		case <-s.Completed:
			return true, nil
		default:
			return false, nil
		}
	}
}
