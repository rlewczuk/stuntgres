package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (s *StatementScope) acquireAdvisory(ctx context.Context, ref lock.Resource, mode lock.Mode, lifetime functions.AdvisoryLifetime, try bool) (bool, error) {
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, NoWait: try, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext, Provenance: s.TxRef}
	ticket := s.Op.Session.Advisory.Ticket
	if lifetime == functions.AdvisoryTransaction {
		ticket = s.LockTicket
	}
	request, err := e.Locks.StartAcquire(ctx, ticket, ref, mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		return false, nil
	}
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
		if err == nil {
			// Retain the original provenance, never retry a replacement name.
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
			e.Mu.Lock()
			if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
		}
	}
	return err == nil, err
}
