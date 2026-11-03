package core

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/statistics"
)

// Admission is held; evaluator/provider work never holds an owner/engine mutex.
// Terminal bodies join the ordinary private segment before the one publisher.
func (e *Engine) checkTransactionConstraints(s *StatementScope) error {
	// A retained immutable journal already owned by the transaction is not a
	// new segment. Keep it available to held observers without merging twice.
	if len(s.constraintModes().Events) == 0 && len(s.Stage.Rows) == 0 && len(s.Stage.Deletes) == 0 && !s.ProjectedRoots && (s.Catalog == nil || s.Catalog == s.Owner.Catalog) {
		return nil
	}
	// A native Commit's borrowed scope has no parsed root command, but its
	// admitted trigger bodies own the same writable stage as an SQL terminal.
	s.worked = true
	if len(s.constraintModes().Events) != 0 {
		if err := s.snapshotDataPhase(); err != nil {
			return err
		}
	}
	if err := s.completeCommandEffects(false, false); err != nil {
		return err
	}
	if err := s.checkConstraintEvents(true, nil); err != nil {
		return err
	}
	if err := s.acquireAlterCatalogNames(); err != nil {
		return err
	}
	if s.Catalog != nil {
		e.Mu.Lock()
		err := s.AcquireJournalRoleDependenciesLocked(s.Catalog)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := s.finishForeignModifications(nil); err != nil {
		return err
	}
	for _, program := range s.rlsPrograms {
		if err := program.FinishSources(nil); err != nil {
			return err
		}
	}
	t := s.Owner
	t.Mu.Lock()
	defer t.Mu.Unlock()
	catalog := t.Catalog
	if s.Catalog != nil {
		catalog = s.Catalog
	}
	return s.mergeSegmentLocked(catalog)
}

// EndLockOwner retires dirty exclusion visibility, then releases the ticket's
// lock ownership, including its completion resource. Neither Engine.mu nor
// the manager mutex is held by the caller.
func (e *Engine) EndLockOwner(ticket *lock.Ticket) {
	if ticket != nil {
		if count := ticket.TakeDeadlocks(); count != 0 {
			e.sessionMu.Lock()
			owner := e.sessions[ticket.Session()]
			e.sessionMu.Unlock()
			if owner != nil {
				addDatabaseStats(e, owner, statistics.DatabaseFact{Deadlocks: count})
			}
		}
	}
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
