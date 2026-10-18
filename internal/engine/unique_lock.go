package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// uniqueKeyValues is a claim's comparer: index ordering decides equality.
type uniqueKeyValues struct {
	ordering indexOrdering
	values   []sqltype.Value
}

func (k *uniqueKeyValues) equalKey(ctx context.Context, stored any) (bool, error) {
	cmp, err := k.ordering.compareKeys(ctx, logicalIndexEntry{keys: stored.([]sqltype.Value)}, logicalIndexEntry{keys: k.values})
	return err == nil && cmp == 0, err
}

func (k *uniqueKeyValues) ownedKey() any { return slices.Clone(k.values) }

// pinUniqueKey hashes the claimed values, then pins their interned lock
// resource. Hashing and comparison run outside every metadata mutex.
func (e *Engine) pinUniqueKey(ctx context.Context, ticket *relationTicket, ref ids.ObjectRef, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (lockResource, func(), error) {
	hash, err := uniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lockResource{}, nil, err
	}
	return e.locks.pinUniqueKey(ctx, ticket, ref, hash, &uniqueKeyValues{ordering: ordering, values: values})
}

// Both lock interning and the conflict command's private overlay use this
// provider bucket. Ordering comparison, never the hash, decides equality.
func uniqueKeyHash(ctx context.Context, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (uint64, error) {
	hash := uint64(14695981039346656037)
	for i, value := range values {
		var h uint64
		class := ordering.keys[i].class
		defaultClass, _ := sqltype.DefaultIndexClass(value.Type())
		// Nondefault equality can be coarser than the type's grouping hash
		// (TEXT bpchar_ops ignores trailing spaces). Compare within one bucket.
		if class.Family != "network_ops" && (class.Name == "" || class.Name == defaultClass.Name) {
			var err error
			h, err = grouping[i].Hash(ctx, value)
			if err != nil {
				return 0, err
			}
		}
		hash = (hash ^ h) * 1099511628211
	}
	return hash, nil
}

func (s *StatementScope) acquireUnique(ref ids.ObjectRef, bound *uniqueBoundKey, key []sqltype.Value) error {
	e := s.op.session.Engine()
	resource, unpin, err := e.pinUniqueKey(s.op.Context(), s.ticket, ref, bound.ordering, bound.grouping, key)
	if err != nil {
		return err
	}
	defer unpin()
	policy := relationLockPolicy{timeoutSlot: &s.op.lockWait, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, resource, uniqueClaim, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
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
