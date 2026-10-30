package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// CopyReplicationOriginProgressLocked detaches logical slot values inside the
// same capture window as the immutable live registrations. Presence includes 0.
func (e *Engine) CopyReplicationOriginProgressLocked() map[ids.OID]uint64 {
	if e.Cluster == nil || len(e.Cluster.replicationOriginProgress) == 0 {
		return nil
	}
	values := make(map[ids.OID]uint64, len(e.Cluster.replicationOriginProgress))
	for id, value := range e.Cluster.replicationOriginProgress {
		values[id] = value
	}
	return values
}

type replicationOriginProgressKey struct{ Name, Subscription string }

func replicationOriginKey(read catalog.Read, d *catalog.ReplicationOrigin) replicationOriginProgressKey {
	if subscription, ok := read.Get(d.Subscription).(*catalog.Subscription); ok {
		return replicationOriginProgressKey{Subscription: subscription.Name()}
	}
	return replicationOriginProgressKey{Name: d.Name()}
}
func (e *Engine) captureReplacementOriginProgressLocked(read catalog.Read) map[replicationOriginProgressKey]uint64 {
	if e.Cluster == nil {
		return nil
	}
	var values map[replicationOriginProgressKey]uint64
	for _, d := range read.ReplicationOrigins() {
		if subscription, ok := read.Get(d.Subscription).(*catalog.Subscription); ok && subscription.Database != e.Catalog.DatabaseAddress() {
			continue
		}
		if position, active := e.Cluster.replicationOriginProgress[d.Address().ObjectID]; active {
			if values == nil {
				values = make(map[replicationOriginProgressKey]uint64)
			}
			values[replicationOriginKey(read, d)] = position
		}
	}
	return values
}

// Prepare the complete current union after the last preparation unlock. No
// catalog, storage, reservation or progress authority changes on failure.
func (e *Engine) prepareReplacementOriginProgressLocked(next *catalog.State, imported map[replicationOriginProgressKey]uint64) (map[ids.OID]uint64, error) {
	if e.Cluster == nil {
		return nil, nil
	}
	read := e.Catalog.Read(next)
	var values map[ids.OID]uint64
	add := func(id ids.OID, position uint64) {
		if values == nil {
			values = make(map[ids.OID]uint64)
		}
		values[id] = position
	}
	for _, d := range read.ReplicationOrigins() {
		position, active := imported[replicationOriginKey(read, d)]
		if subscription, ok := read.Get(d.Subscription).(*catalog.Subscription); ok && subscription.Database != e.Catalog.DatabaseAddress() {
			position, active = e.Cluster.replicationOriginProgress[d.Address().ObjectID]
		}
		if active {
			add(d.Address().ObjectID, position)
		}
	}
	for id, reservation := range e.Cluster.replicationOrigins {
		if !e.retainedPeerOriginReservationLocked(reservation) {
			continue
		}
		if position, active := e.Cluster.replicationOriginProgress[id]; active {
			add(id, position)
		}
	}
	definition, err := e.Settings.Lookup("max_active_replication_origins")
	if err != nil {
		return nil, err
	}
	capacity := int64(definition.DefaultValue().Number)
	if int64(len(values)) > capacity {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: fmt.Sprintf(`could not replace replication origin progress: replacement requires %d active replication state slots, but "max_active_replication_origins" is %d`, len(values), capacity), Hint: `Increase "max_active_replication_origins" and try again.`}
	}
	return values, nil
}

// Scope/liveness only: never inspect a foreign owner's mutable journal.
func (e *Engine) retainedPeerOriginReservationLocked(reservation replicationOriginReservation) bool {
	if reservation.SubscriptionDatabase == (ids.ObjectAddress{}) || reservation.SubscriptionDatabase == e.Catalog.DatabaseAddress() {
		return false
	}
	for store := range e.Cluster.Catalogs {
		if store.Domain == reservation.Ref.Domain() && store.DatabaseAddress() == reservation.SubscriptionDatabase && store.State.Epoch() == reservation.Ref.Epoch() {
			return true
		}
	}
	return false
}

type originProgressPredecessor struct{ Standalone, Selected uint64 }

func (e *Engine) originProgressPredecessorLocked() originProgressPredecessor {
	if e.Cluster == nil {
		return originProgressPredecessor{}
	}
	generation := e.Cluster.replicationOriginProgressGeneration
	return originProgressPredecessor{Standalone: generation[ids.ObjectAddress{}], Selected: generation[e.Catalog.DatabaseAddress()]}
}
func (e *Engine) bumpOriginProgressScopeLocked(scope ids.ObjectAddress) {
	if e.Cluster == nil {
		return
	}
	if e.Cluster.replicationOriginProgressGeneration == nil {
		e.Cluster.replicationOriginProgressGeneration = make(map[ids.ObjectAddress]uint64)
	}
	e.Cluster.replicationOriginProgressGeneration[scope]++
}
func (e *Engine) bumpOriginProgressLocked(read catalog.Read, d *catalog.ReplicationOrigin) {
	scope := ids.ObjectAddress{}
	if reservation, ok := e.Cluster.replicationOrigins[d.Address().ObjectID]; ok {
		scope = reservation.SubscriptionDatabase
	} else if e.Catalog.Current().Get(d.Address()) == d {
		if subscription, ok := read.Get(d.Subscription).(*catalog.Subscription); ok {
			scope = subscription.Database
		}
	}
	e.bumpOriginProgressScopeLocked(scope)
}
