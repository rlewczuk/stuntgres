package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lock.Resource, mode lock.Mode, lifetime functions.AdvisoryLifetime, try bool) (bool, error) {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, NoWait: try, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext, Provenance: s.txRef}
	ticket := s.op.Session.advisory.ticket
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
