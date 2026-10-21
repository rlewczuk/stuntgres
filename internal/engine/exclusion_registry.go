package engine

import (
	"stuntgres/internal/engine/lock"
)

func (e *Engine) checkTransactionExclusions(s *StatementScope) error {
	if err := s.checkExclusionEvents(true, nil); err != nil {
		return err
	}
	ownerExtensionOf(s.owner).exclusionModes = s.indexesOrZero().exclusionModes
	return nil
}

// endLockOwner retires dirty exclusion visibility, then releases the ticket's
// lock ownership, including its completion resource. Neither Engine.mu nor
// the manager mutex is held by the caller.
func (e *Engine) endLockOwner(ticket *lock.Ticket) {
	if ticket != nil && ticket.Ref().Domain() != nil {
		e.removeExclusionOwner(ticket)
	}
	e.locks.End(ticket)
}

func (e *Engine) removeExclusionOwner(ticket *lock.Ticket) {
	e.mu.Lock()
	e.exclusions.removeLocked(ticket)
	e.mu.Unlock()
}
