package core

import (
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func originReplacementOwnerFixture() (*Engine, *catalog.Store, *session.TransactionOwner) {
	e := &Engine{Settings: settings.NewRegistry(nil, false)}
	e.Catalog = catalog.NewStore(catalog.Owner{}, 20000, 1)
	e.Catalog.State = catalog.NewState(1)
	peerEngine := &Engine{}
	peerEngine.Catalog = catalog.NewStore(catalog.Owner{Database: ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 20001}}, 20002, 1)
	peerEngine.Catalog.State = catalog.NewState(3)
	e.Cluster = &DatabaseCluster{Targets: map[string]*Engine{"selected": e, "peer": peerEngine}, replicationOrigins: make(map[ids.OID]replicationOriginReservation), replicationOriginProgress: make(map[ids.OID]uint64)}
	owner := &session.TransactionOwner{}
	owner.Ref = peerEngine.Catalog.Domain.Tx(peerEngine.Catalog.Domain.Session(1), 1, peerEngine.Catalog.State.Epoch(), owner, 1)
	return e, &peerEngine.Catalog, owner
}

func TestReplicationOriginStateReplacementPendingPeers(t *testing.T) {
	for _, position := range []uint64{0, 0x8000000000000008} {
		e, peer, owner := originReplacementOwnerFixture()
		address := ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 1}
		d := &catalog.ReplicationOrigin{ObjectAddress: address, ObjectRevision: 1, ObjectName: "pg_pending"}
		stage := &CatalogStage{Owner: e, replicationOrigins: []ids.OID{1}}
		e.Cluster.replicationOrigins[1] = replicationOriginReservation{Ref: owner.Ref, Address: address, Stage: stage, SubscriptionDatabase: peer.DatabaseAddress()}
		e.Cluster.replicationOriginProgress[1] = position
		check := func(active bool) {
			t.Helper()
			values, err := e.prepareReplacementOriginProgressLocked(catalog.CloneState(e.Catalog.State), nil)
			value, present := values[1]
			if err != nil || present != active || active && value != position {
				t.Fatal(values, err, active)
			}
			if e.Cluster.replicationOrigins[1].SubscriptionDatabase != peer.DatabaseAddress() {
				t.Fatal("scope lost")
			}
		}
		check(true)
		e.transferReplicationOriginsLocked(stage, owner.Ref)
		journal := session.NewJournal()
		journal.Descriptors[address] = d
		owner.Catalog = journal
		e.reconcileReplicationOriginsLocked(owner)
		check(true)
		predecessor := e.originProgressPredecessorLocked()
		e.bumpOriginProgressLocked(peer.Current(), d)
		if e.originProgressPredecessorLocked() != predecessor || e.Cluster.replicationOriginProgressGeneration[peer.DatabaseAddress()] != 1 {
			t.Fatal("pending peer classified standalone")
		}
		owner.Savepoints = []session.SavepointMark{{Catalog: journal}}
		owner.Catalog = journal.Clone()
		owner.Catalog.Descriptors[address] = nil
		e.reconcileReplicationOriginsLocked(owner)
		check(true)
		// Immediate DROP clears even while the descriptor is revivable.
		delete(e.Cluster.replicationOriginProgress, 1)
		check(false)
		owner.Catalog, owner.Savepoints = journal, nil
		e.reconcileReplicationOriginsLocked(owner)
		check(false)
		owner.Catalog = nil
		owner.Savepoints = []session.SavepointMark{{StatementCatalog: journal}}
		e.reconcileReplicationOriginsLocked(owner)
		check(false)
		owner.Savepoints = nil
		e.reconcileStatementReplicationOriginsLocked(owner, journal)
		check(false)
		e.Cluster.replicationOriginProgress[1] = position
		e.prepareReplicationOriginsLocked(owner.Ref, true)
		e.releaseReplicationOriginsLocked(owner.Ref)
		check(true)
		e.prepareReplicationOriginsLocked(owner.Ref, false)
		e.releaseReplicationOriginsLocked(owner.Ref)
		if len(e.Cluster.replicationOrigins) != 0 {
			t.Fatal("settlement leaked reservation")
		}
		// No claim means orphan writes are global again.
		e.bumpOriginProgressLocked(peer.Current(), d)
		if e.originProgressPredecessorLocked().Standalone != predecessor.Standalone+1 {
			t.Fatal("orphan retained peer classification")
		}
	}
}

func TestReplicationOriginStateReplacementCapacity(t *testing.T) {
	for _, count := range []int{9, 8} {
		e, peer, owner := originReplacementOwnerFixture()
		for i := range count {
			id := ids.OID(i + 1)
			e.Cluster.replicationOrigins[id] = replicationOriginReservation{Ref: owner.Ref, Address: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: id}, SubscriptionDatabase: peer.DatabaseAddress()}
			e.Cluster.replicationOriginProgress[id] = uint64(i)
		}
		// Scoped but inactive reservations do not consume a slot. Standalone
		// and selected claims, including stale ones, are not retained peers.
		e.Cluster.replicationOrigins[50] = replicationOriginReservation{Ref: owner.Ref, SubscriptionDatabase: peer.DatabaseAddress()}
		e.Cluster.replicationOrigins[51] = replicationOriginReservation{Ref: owner.Ref}
		e.Cluster.replicationOriginProgress[51] = 99
		selected := e.Catalog.Domain.Tx(e.Catalog.Domain.Session(1), 1, 1, nil, 1)
		e.Cluster.replicationOrigins[52] = replicationOriginReservation{Ref: selected, SubscriptionDatabase: e.Catalog.DatabaseAddress()}
		e.Cluster.replicationOriginProgress[52] = 88
		stale := peer.Domain.Tx(peer.Domain.Session(2), 2, 1, nil, 2)
		e.Cluster.replicationOrigins[53] = replicationOriginReservation{Ref: stale, SubscriptionDatabase: peer.DatabaseAddress()}
		e.Cluster.replicationOriginProgress[53] = 77
		next := catalog.CloneState(e.Catalog.State)
		// A published/pending overlap is one slot, not two.
		sub := &catalog.Subscription{ObjectAddress: ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: 20003}, ObjectRevision: 1, ObjectName: "overlap", Database: peer.DatabaseAddress(), ReplicationOriginID: 1}
		published := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 1}, ObjectRevision: 1, ObjectName: sub.ReplicationOriginName(), Subscription: sub.Address()}
		next.SetDescriptor(sub.Address(), sub)
		next.SetDescriptor(published.Address(), published)
		imported := map[replicationOriginProgressKey]uint64{{Name: "high"}: 0x8000000000000004, {Name: "zero"}: 0}
		for i, name := range []string{"high", "zero"} {
			d := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: ids.OID(60 + i)}, ObjectRevision: 1, ObjectName: name}
			next.SetDescriptor(d.Address(), d)
			next.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
		root, peerRoot, oid := e.Catalog.State, peer.State, e.Catalog.NextOID()
		claims, progress, generations := maps.Clone(e.Cluster.replicationOrigins), maps.Clone(e.Cluster.replicationOriginProgress), maps.Clone(e.Cluster.replicationOriginProgressGeneration)
		values, err := e.prepareReplacementOriginProgressLocked(next, imported)
		if count == 9 {
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: `could not replace replication origin progress: replacement requires 11 active replication state slots, but "max_active_replication_origins" is 10`, Hint: `Increase "max_active_replication_origins" and try again.`}
			if !reflect.DeepEqual(err, want) || values != nil {
				t.Fatal(values, err)
			}
		} else {
			if err != nil || len(values) != 10 || values[60] != 0x8000000000000004 {
				t.Fatal(values, err)
			}
			for _, id := range []ids.OID{1, 61} {
				if value, ok := values[id]; !ok || value != 0 {
					t.Fatal("allocated zero lost", id, values)
				}
			}
			if _, ok := values[50]; ok {
				t.Fatal("inactive reservation allocated")
			}
		}
		if root != e.Catalog.State || peerRoot != peer.State || oid != e.Catalog.NextOID() || !maps.Equal(claims, e.Cluster.replicationOrigins) || !maps.Equal(progress, e.Cluster.replicationOriginProgress) || !maps.Equal(generations, e.Cluster.replicationOriginProgressGeneration) {
			t.Fatal("preparation mutated destination authority")
		}
		e.Catalog.State.SetEpoch(2)
		e.retireReplacedReplicationOriginsLocked()
		if _, exists := e.Cluster.replicationOrigins[52]; exists {
			t.Fatal("selected stale claim survived retirement")
		}
		if _, exists := e.Cluster.replicationOrigins[1]; !exists {
			t.Fatal("peer claim retired")
		}
	}
	e, _, _ := originReplacementOwnerFixture()
	e.Settings = settings.NewRegistry([]settings.Definition{{Name: "max_active_replication_origins", Type: settings.Integer, BootDefault: "0"}}, false)
	if values, err := e.prepareReplacementOriginProgressLocked(catalog.CloneState(e.Catalog.State), nil); err != nil || values != nil {
		t.Fatal("empty replacement at zero capacity", values, err)
	}
}
