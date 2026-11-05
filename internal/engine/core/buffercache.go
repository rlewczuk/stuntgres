package core

import (
	"stuntgres/internal/engine/ids"
	"sync"
)

// logicalBufferCache is a cluster-owned leaf: callers may hold Engine.Mu, but
// this mutex never acquires engine/session/catalog authority or invokes code.
// Maps are sparse; neither construction nor snapshots allocate capacity rows.
type logicalBufferCache struct {
	mu       sync.Mutex
	capacity int64
	cursor   int64
	nextFree int64
	byKey    map[logicalBufferKey]*logicalBufferEntry
	bySlot   map[int64]*logicalBufferEntry
}
type logicalBufferKey struct {
	domain                *ids.Domain
	epoch                 ids.CatalogEpoch
	relation              ids.ObjectAddress
	node, space, database ids.OID
}
type logicalBufferEntry struct {
	key         logicalBufferKey
	slot, usage int64
	owners      map[ids.SessionToken]int
}
type logicalBufferRow struct {
	node, space, database ids.OID
	usage, pins           int64
}

func (c *logicalBufferCache) open(key logicalBufferKey, owner ids.SessionToken) (*logicalBufferEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.byKey[key]
	resident := entry != nil
	if entry == nil {
		if c.capacity <= 0 {
			return nil, false
		}
		slot := int64(0)
		if int64(len(c.bySlot)) < c.capacity {
			// Reuse the lowest vacant ID, including holes left by retirement.
			c.nextFree = max(c.nextFree, 1)
			for c.bySlot[c.nextFree] != nil {
				c.nextFree++
			}
			slot = c.nextFree
			c.nextFree++
		} else {
			for range c.capacity {
				c.cursor = c.cursor%c.capacity + 1
				victim := c.bySlot[c.cursor]
				if len(victim.owners) == 0 {
					slot = c.cursor
					delete(c.byKey, victim.key)
					break
				}
			}
		}
		if slot == 0 {
			return nil, false
		} // All occupied slots have live leases.
		if c.byKey == nil {
			c.byKey = make(map[logicalBufferKey]*logicalBufferEntry)
			c.bySlot = make(map[int64]*logicalBufferEntry)
		}
		entry = &logicalBufferEntry{key: key, slot: slot}
		c.byKey[key], c.bySlot[slot] = entry, entry
	}
	entry.usage = min(entry.usage+1, 5)
	if entry.owners == nil {
		entry.owners = make(map[ids.SessionToken]int)
	}
	entry.owners[owner]++
	return entry, resident
}
func (c *logicalBufferCache) close(entry *logicalBufferEntry, owner ids.SessionToken) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Pointer identity fences a removed incarnation and a reused slot without
	// wrapping counters or decrementing the replacement's lease.
	if c.bySlot[entry.slot] != entry || c.byKey[entry.key] != entry {
		return
	}
	if n := entry.owners[owner]; n > 1 {
		entry.owners[owner] = n - 1
	} else {
		delete(entry.owners, owner)
	}
}
func (c *logicalBufferCache) snapshot() (int64, map[int64]logicalBufferRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bySlot) == 0 {
		return c.capacity, nil
	}
	rows := make(map[int64]logicalBufferRow, len(c.bySlot))
	for slot, entry := range c.bySlot {
		rows[slot] = logicalBufferRow{node: entry.key.node, space: entry.key.space, database: entry.key.database, usage: entry.usage, pins: int64(len(entry.owners))}
	}
	return c.capacity, rows
}
func (c *logicalBufferCache) touch(entry *logicalBufferEntry) bool {
	if entry == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bySlot[entry.slot] == entry && c.byKey[entry.key] == entry {
		entry.usage = min(entry.usage+1, 5)
		return true
	}
	return false
}
func (c *logicalBufferCache) retireLocked(key logicalBufferKey, entry *logicalBufferEntry) {
	delete(c.byKey, key)
	delete(c.bySlot, entry.slot)
	c.nextFree = min(c.nextFree, entry.slot)
	entry.owners = nil // No session tokens survive successful retirement.
}

// Caller has resolved authority and storage before entering this leaf mutex.
func (c *logicalBufferCache) evictSlot(slot int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.bySlot[slot]
	if entry == nil || len(entry.owners) != 0 {
		return false
	}
	c.retireLocked(entry.key, entry)
	return true
}

func (c *logicalBufferCache) evictMatching(key *logicalBufferKey) (evicted, skipped int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for candidate, entry := range c.byKey {
		if key != nil && candidate != *key {
			continue
		}
		if len(entry.owners) != 0 {
			skipped++
			continue
		}
		c.retireLocked(candidate, entry)
		evicted++
	}
	return
}
