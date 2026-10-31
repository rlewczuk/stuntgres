package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"time"
)

// Replacement initializes estimates; replay is not a collected tuple event.
// This runs only at the accepted, callback-free installation boundary. Existing
// destination TEMP identities retain published monitoring facts, not the physical
// maintenance counters (which may include deliberately untracked operations).
func (e *Engine) replaceStatsLocked(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation) {
	facts := make(map[statistics.Key]statistics.Fact, len(relations))
	oldEpoch := e.Catalog.State.Epoch()
	for address, store := range relations {
		key := statistics.Key{Object: e.Catalog.Domain.Ref(oldEpoch+1, address), Category: statistics.Relation}
		fact := statistics.Fact{Live: store.Maintenance.Live}
		if d, ok := next.Relation(address); ok {
			if ns, ok := e.Catalog.Read(next).Namespace(d.Namespace()); ok && ns.Temporary.Domain() == e.Catalog.Domain && session.TokenAttached(ns.Temporary) {
				fact = e.Statistics.Read(statistics.Key{Object: e.Catalog.Domain.Ref(oldEpoch, address), Category: statistics.Relation})
			}
		}
		facts[key] = fact
	}
	e.Statistics.Replace(time.Now().UTC().Truncate(time.Microsecond), facts)
}
