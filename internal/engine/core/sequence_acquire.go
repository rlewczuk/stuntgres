package core

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (v AuthorizationView) checkSequenceAny(ref ids.ObjectRef, mask authorization.Privileges) error {
	d, err := v.Object(ref)
	if err != nil {
		return err
	}
	rights, err := v.Effective(ref, false)
	if err != nil {
		return err
	}
	if rights&mask != 0 {
		return nil
	}
	return SQLError("42501", fmt.Sprintf("permission denied for sequence %s", d.Name()))
}

func (s *StatementScope) AcquireSequence(ref ids.ObjectRef, mode lock.Mode, reserveReplacement bool) error {
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, mode, policy)
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if reserveReplacement && s.implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.markTemporaryAccessLocked(s, ref, temporaryOwnAccess)
	return nil
}
