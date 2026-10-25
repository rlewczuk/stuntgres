package engine

import (
	"stuntgres/internal/engine/lock"
)

func (e *Engine) checkTransactionExclusions(s *StatementScope) error {
	if err := s.checkExclusionEvents(true, nil); err != nil {
		return err
	}
	ownerExtensionOf(s.Owner).exclusionModes = s.IndexesOrZero().ExclusionModes
	return nil
}

// EndLockOwner retires dirty exclusion visibility, then releases the ticket's
// lock ownership, including its completion resource. Neither Engine.mu nor
// the manager mutex is held by the caller.
func (e *Engine) EndLockOwner(ticket *lock.Ticket) {
	if ticket != nil && ticket.Ref().Domain() != nil {
		e.removeExclusionOwner(ticket)
	}
	e.Locks.End(ticket)
}

func (e *Engine) removeExclusionOwner(ticket *lock.Ticket) {
	e.Mu.Lock()
	e.Exclusions.RemoveLocked(ticket)
	e.Mu.Unlock()
}
