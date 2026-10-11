package engine

import (
	"context"
	"slices"
	"stuntgres/internal/sqltype"
)

type uniqueLockBucket struct {
	ObjectRef
	hash uint64
}
type uniqueLockBucketState struct {
	generation uint64
	keys       []*uniqueLockKey
}
type uniqueLockKey struct {
	bucket uniqueLockBucket
	values []sqltype.Value
}

// Called only with the existing lock owner's mutex. Empty buckets disappear;
// their pointer identity fences remove/recreate without a permanent tombstone.
func (m *relationLockManager) retireUniqueKey(key *uniqueLockKey) {
	bucket := m.keyBuckets[key.bucket]
	if bucket == nil {
		return
	}
	at := slices.Index(bucket.keys, key)
	if at < 0 {
		return
	}
	bucket.keys = slices.Delete(bucket.keys, at, at+1)
	bucket.generation++
	if len(bucket.keys) == 0 {
		delete(m.keyBuckets, key.bucket)
	}
}

// Both lock interning and the conflict command's private overlay use this
// provider bucket. Ordering comparison, never the hash, decides equality.
func uniqueKeyHash(ctx context.Context, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (uint64, error) {
	hash := uint64(14695981039346656037)
	for i, value := range values {
		var h uint64
		class := ordering.keys[i].class
		defaultClass, _ := sqltype.DefaultIndexClass(value.Type())
		// Nondefault equality can be coarser than the type's grouping hash.
		if class.Name == "" || class.Name == defaultClass.Name {
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

// Providers run outside every manager/engine/session metadata mutex. Pinning
// reserves the exact interned resource across startAcquire; its normal entry
// owns checkpoint, queue, timeout, deadlock, outcome and epoch lifetimes.
func (m *relationLockManager) pinUniqueKey(ctx context.Context, ticket *relationTicket, ref ObjectRef, ordering indexOrdering, grouping []sqltype.Grouping, values []sqltype.Value) (lockResource, func(), error) {
	hash, err := uniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lockResource{}, nil, err
	}
	identity := uniqueLockBucket{ObjectRef: ref, hash: hash}
	for {
		if err := ctx.Err(); err != nil {
			return lockResource{}, nil, err
		}
		m.mu.Lock()
		if !m.valid(ticket) || ref.Epoch != ticket.ref.epoch {
			m.mu.Unlock()
			return lockResource{}, nil, ErrCatalogReplaced
		}
		bucket := m.keyBuckets[identity]
		var generation uint64
		var candidates []*uniqueLockKey
		if bucket != nil {
			generation = bucket.generation
			candidates = slices.Clone(bucket.keys)
		}
		m.mu.Unlock()
		var found *uniqueLockKey
		for _, key := range candidates {
			cmp, err := ordering.compareKeys(ctx, logicalIndexEntry{keys: key.values}, logicalIndexEntry{keys: values})
			if err != nil {
				return lockResource{}, nil, err
			}
			if cmp == 0 {
				found = key
				break
			}
		}
		m.mu.Lock()
		if !m.valid(ticket) {
			m.mu.Unlock()
			return lockResource{}, nil, ErrCatalogReplaced
		}
		current := m.keyBuckets[identity]
		if current != bucket || current != nil && current.generation != generation {
			m.mu.Unlock()
			continue
		}
		if found == nil {
			if bucket == nil {
				bucket = &uniqueLockBucketState{}
				if m.keyBuckets == nil {
					m.keyBuckets = make(map[uniqueLockBucket]*uniqueLockBucketState)
				}
				m.keyBuckets[identity] = bucket
			}
			found = &uniqueLockKey{bucket: identity, values: slices.Clone(values)}
			bucket.keys = append(bucket.keys, found)
			bucket.generation++
		}
		resource := lockResource{ObjectRef: ref, key: found}
		entry := m.entries[resource]
		if entry == nil {
			entry = &relationEntry{holders: make(map[*relationTicket]relationHold)}
			m.entries[resource] = entry
		}
		entry.pins++
		m.mu.Unlock()
		unpin := func() {
			m.mu.Lock()
			entry.pins--
			if m.entries[resource] == entry {
				m.retire(resource, entry)
			}
			m.mu.Unlock()
		}
		return resource, unpin, nil
	}
}

func (s *StatementScope) acquireUnique(ref ObjectRef, bound *uniqueBoundKey, key []sqltype.Value) error {
	e := s.op.session.Engine()
	resource, unpin, err := e.locks.pinUniqueKey(s.op.Context(), s.ticket, ref, bound.ordering, bound.grouping, key)
	if err != nil {
		return err
	}
	defer unpin()
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
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
	return nil
}
