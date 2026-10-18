package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to relationLockManager.
type sessionAdvisory struct {
	ticket *relationTicket
	locks  *relationLockManager
}

func (a *sessionAdvisory) EndSession(token ids.SessionToken, _ EndReason) error {
	if a.ticket.Session() != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.locks.End(a.ticket)
	return nil
}

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lockResource, mode relationLockMode, lifetime advisoryLifetime, try bool) (bool, error) {
	e := s.op.session.Engine()
	policy := relationLockPolicy{TimeoutSlot: &s.op.lockWait, NoWait: try, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext, Provenance: s.txRef}
	ticket := s.op.session.advisory.ticket
	if lifetime == advisoryTransaction {
		ticket = s.ticket
	}
	request, err := e.locks.StartAcquire(ctx, ticket, ref, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
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
			if s.txRef.Epoch() != e.state.epoch {
				err = ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
	}
	return err == nil, err
}
