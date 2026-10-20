package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to lock.Manager.
type sessionAdvisory struct {
	ticket *lock.Ticket
	locks  *lock.Manager
}

func (a *sessionAdvisory) EndSession(token ids.SessionToken, _ EndReason) error {
	if a.ticket.Session() != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.locks.End(a.ticket)
	return nil
}

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lock.Resource, mode lock.Mode, lifetime functions.AdvisoryLifetime, try bool) (bool, error) {
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, NoWait: try, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext, Provenance: s.txRef}
	ticket := s.op.session.advisory.ticket
	if lifetime == functions.AdvisoryTransaction {
		ticket = s.ticket
	}
	request, err := e.locks.StartAcquire(ctx, ticket, ref, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		return false, nil
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
		if err == nil {
			// Retain the original provenance, never retry a replacement name.
			e.replacementMu.RLock()
			s.replacementRead = true
			e.mu.Lock()
			if s.txRef.Epoch() != e.catalog.state.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
	}
	return err == nil, err
}
