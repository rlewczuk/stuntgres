package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/ids"
)

type uniqueLockBucket struct {
	ids.ObjectRef
	hash uint64
}

type uniqueLockBucketState struct {
	generation uint64
	keys       []*uniqueLockKey
}

type uniqueLockKey struct {
	bucket uniqueLockBucket
	value  any // owned key value; only the claim's comparer interprets it
}

// uniqueKeyComparer decides key equality for one claim. The manager calls it
// only outside its mutex, so a comparison may evaluate providers.
type uniqueKeyComparer interface {
	equalKey(ctx context.Context, stored any) (bool, error)
	ownedKey() any // an owned copy of the claimed value, interned when new
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

// Providers run outside every manager/engine/session metadata mutex. Pinning
// reserves the exact interned resource across startAcquire; its normal entry
// owns checkpoint, queue, timeout, deadlock, outcome and epoch lifetimes.
func (m *relationLockManager) pinUniqueKey(ctx context.Context, ticket *relationTicket, ref ids.ObjectRef, hash uint64, claim uniqueKeyComparer) (lockResource, func(), error) {
	identity := uniqueLockBucket{ObjectRef: ref, hash: hash}
	for {
		if err := ctx.Err(); err != nil {
			return lockResource{}, nil, err
		}
		m.mu.Lock()
		if !m.valid(ticket) || ref.Epoch != ticket.ref.Epoch() {
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
			equal, err := claim.equalKey(ctx, key.value)
			if err != nil {
				return lockResource{}, nil, err
			}
			if equal {
				found = key
				break
			}
		}
		var owned any
		if found == nil {
			owned = claim.ownedKey()
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
			found = &uniqueLockKey{bucket: identity, value: owned}
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
