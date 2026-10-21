package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// pinUniqueKey hashes the claimed values, then pins their interned lock
// resource. Hashing and comparison run outside every metadata mutex.
func (e *Engine) pinUniqueKey(ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	hash, err := UniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lock.Resource{}, nil, err
	}
	return e.locks.PinUniqueKey(ctx, ticket, ref, hash, &uniqueKeyValues{Ordering: ordering, Values: values})
}

func (s *StatementScope) acquireUnique(ref ids.ObjectRef, bound *uniqueBoundKey, key []sqltype.Value) error {
	e := s.engine()
	resource, unpin, err := e.pinUniqueKey(s.op.Context(), s.ticket, ref, bound.Ordering, bound.Grouping, key)
	if err != nil {
		return err
	}
	defer unpin()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, resource, lock.UniqueClaim, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	if request != nil {
		return s.readmitProgramsAfterWait()
	}
	return nil
}
