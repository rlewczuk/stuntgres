package lock

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
	keys       []*UniqueKey
}

type UniqueKey struct {
	bucket uniqueLockBucket
	value  any // owned key value; only the claim's comparer interprets it
}

// UniqueKeyComparer decides key equality for one claim. The manager calls it
// only outside its mutex, so a comparison may evaluate providers.
type UniqueKeyComparer interface {
	EqualKey(ctx context.Context, stored any) (bool, error)
	OwnedKey() any // an owned copy of the claimed value, interned when new
}

// Called only with the existing lock owner's mutex. Empty buckets disappear;
// their pointer identity fences remove/recreate without a permanent tombstone.
func (m *Manager) retireUniqueKey(key *UniqueKey) {
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
// reserves the exact interned resource across StartAcquire; its normal entry
// owns checkpoint, queue, timeout, deadlock, outcome and epoch lifetimes.
func (m *Manager) PinUniqueKey(ctx context.Context, ticket *Ticket, ref ids.ObjectRef, hash uint64, claim UniqueKeyComparer) (Resource, func(), error) {
	identity := uniqueLockBucket{ObjectRef: ref, hash: hash}
	for {
		if err := ctx.Err(); err != nil {
			return Resource{}, nil, err
		}
		m.mu.Lock()
		if !m.valid(ticket) || ref.Epoch != ticket.ref.Epoch() {
			m.mu.Unlock()
			return Resource{}, nil, ids.ErrCatalogReplaced
		}
		bucket := m.keyBuckets[identity]
		var generation uint64
		var candidates []*UniqueKey
		if bucket != nil {
			generation = bucket.generation
			candidates = slices.Clone(bucket.keys)
		}
		m.mu.Unlock()
		var found *UniqueKey
		for _, key := range candidates {
			equal, err := claim.EqualKey(ctx, key.value)
			if err != nil {
				return Resource{}, nil, err
			}
			if equal {
				found = key
				break
			}
		}
		var owned any
		if found == nil {
			owned = claim.OwnedKey()
		}
		m.mu.Lock()
		if !m.valid(ticket) {
			m.mu.Unlock()
			return Resource{}, nil, ids.ErrCatalogReplaced
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
			found = &UniqueKey{bucket: identity, value: owned}
			bucket.keys = append(bucket.keys, found)
			bucket.generation++
		}
		resource := Resource{ObjectRef: ref, key: found}
		entry := m.entries[resource]
		if entry == nil {
			entry = &relationEntry{holders: make(map[*Ticket]Hold)}
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
