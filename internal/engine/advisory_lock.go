package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
)

// This delegate owns only the physical lifetime handle. Counts, granted modes,
// queues and terminal decisions belong exclusively to relationLockManager.
type sessionAdvisory struct{ ticket *relationTicket }

func (a *sessionAdvisory) EndSession(token ids.SessionToken, _ EndReason) error {
	if a.ticket.session != token {
		return errors.New("stuntgres: foreign advisory session")
	}
	a.ticket.manager.end(a.ticket)
	return nil
}

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lockResource, mode relationLockMode, lifetime advisoryLifetime, try bool) (bool, error) {
	e := s.op.session.Engine()
	policy := relationLockPolicy{timeoutSlot: &s.op.lockWait, nowait: try, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext, provenance: s.txRef}
	ticket := s.op.session.advisory.ticket
	if lifetime == advisoryTransaction {
		ticket = s.ticket
	}
	request, err := e.locks.startAcquire(ctx, ticket, ref, mode, policy)
	if errors.Is(err, errRelationUnavailable) {
		return false, nil
	}
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
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
