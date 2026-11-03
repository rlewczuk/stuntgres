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
	// Sequences have generators, not heap stores. Preserve only actual
	// destination TEMP sequences whose physical owners remain attached.
	for _, descriptor := range e.Catalog.Read(next).Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || d.RelationKind != catalog.SequenceRelation {
			continue
		}
		ns, ok := e.Catalog.Read(next).Namespace(d.Namespace())
		if !ok || ns.Temporary.Domain() != e.Catalog.Domain || !session.TokenAttached(ns.Temporary) {
			continue
		}
		oldKey := statistics.Key{Object: e.Catalog.Domain.Ref(oldEpoch, d.Address()), Category: statistics.Relation}
		nextKey := statistics.Key{Object: e.Catalog.Domain.Ref(oldEpoch+1, d.Address()), Category: statistics.Relation}
		facts[nextKey] = e.Statistics.Read(oldKey)
	}
	for address, subscription := range e.Catalog.Read(next).Subscriptions() {
		if subscription.Database == e.Catalog.DatabaseAddress() {
			facts[statistics.Key{Object: e.Catalog.Domain.Ref(oldEpoch+1, address), Category: statistics.Subscription}] = statistics.Fact{}
		}
	}
	e.Statistics.Replace(time.Now().UTC().Truncate(time.Microsecond), facts)
	// CREATE DATABASE copies its template into an unregistered runtime. It has
	// neither a cluster nor a published database identity/history to clear yet.
	if e.Cluster != nil {
		e.Cluster.StatementStatistics.Clear(statistics.StatementFilter{DatabaseID: uint32(e.Catalog.DatabaseAddress().ObjectID)}, false, time.Now())
	}
}
