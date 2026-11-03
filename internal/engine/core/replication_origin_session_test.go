package core

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func originSessionUnitFixture(track bool) (*Engine, *session.Session, *catalog.ReplicationOrigin) {
	e := &Engine{Mu: &sync.Mutex{}, Visibility: visibility.NewRegistry(track)}
	e.Catalog = catalog.NewStore(catalog.Owner{}, 20000, 1)
	e.Catalog.State = catalog.NewState(1)
	e.Cluster = &DatabaseCluster{Targets: map[string]*Engine{"selected": e}}
	a := &sessionAllocation{}
	s := &a.session
	s.Payload = &a.payload
	s.Host = (*sessionHost)(e)
	s.SessionToken = e.Catalog.Domain.Session(1)
	t := &session.TransactionOwner{Session: s, Extension: &a.owner}
	s.Transactions = t
	t.Ref = e.Catalog.Domain.Tx(s.Token(), 1, 1, t, 1)
	d := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 1}, ObjectName: "unit_session", ObjectRevision: 1}
	e.Catalog.State.SetDescriptor(d.Address(), d)
	e.Catalog.State.BindName(catalog.DescriptorNameKey(d), d.Address())
	return e, s, d
}
func originSessionUnitSelect(t *testing.T, e *Engine, s *session.Session, d *catalog.ReplicationOrigin) {
	t.Helper()
	if err := e.setupOriginSessionLocked(s, e.Catalog.Current(), d, 10); err != nil {
		t.Fatal(err)
	}
	if e.Cluster.replicationOriginOwners[d.Address().ObjectID] != &PayloadOf(s).origin {
		t.Fatal("index does not point into physical allocation")
	}
}
func originSessionUnitCode(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatal(err, code)
	}
}
func TestReplicationOriginSessionOwnership(t *testing.T) {
	e, s, d := originSessionUnitFixture(false)
	originSessionUnitSelect(t, e, s, d)
	c := &PayloadOf(s).origin
	if v, present := e.Cluster.replicationOriginProgress[c.id]; !present || v != 0 {
		t.Fatal("selection did not reserve a genuine zero slot")
	}
	before := *c
	originSessionUnitCode(t, e.setupOriginSessionLocked(s, e.Catalog.Current(), d, 10), "55000")
	peerAllocation := &sessionAllocation{}
	peer := &peerAllocation.session
	peer.Payload = &peerAllocation.payload
	peer.SessionToken = e.Catalog.Domain.Session(2)
	originSessionUnitCode(t, e.setupOriginSessionLocked(peer, e.Catalog.Current(), d, 10), "55006")
	if *c != before || len(e.Cluster.replicationOriginOwners) != 1 {
		t.Fatal("failed setup changed owner")
	}
	e.releaseOriginSessionLocked(s, peer.Token())
	if *c != before {
		t.Fatal("foreign token released ownership")
	}
	e.releaseOriginSessionLocked(s, s.Token())
	e.releaseOriginSessionLocked(s, s.Token())
	if *c != (replicationOriginSession{}) || len(e.Cluster.replicationOriginOwners) != 0 || len(e.Cluster.replicationOriginProgress) != 1 {
		t.Fatal("release deleted slot or retained context")
	}
	// A retained zero slot can be reselected at exact capacity. A new one cannot.
	if err := e.setupOriginSessionLocked(peer, e.Catalog.Current(), d, 1); err != nil {
		t.Fatal(err)
	}
	another := *d
	another.ObjectAddress.ObjectID = 2
	originSessionUnitCode(t, e.setupOriginSessionLocked(s, e.Catalog.Current(), &another, 1), "53400")
	if PayloadOf(s).origin.id != 0 || len(e.Cluster.replicationOriginProgress) != 1 {
		t.Fatal("capacity failure mutated context")
	}
}
func TestReplicationOriginSessionPublication(t *testing.T) {
	for _, track := range []bool{false, true} {
		t.Run(map[bool]string{false: "tracking_off", true: "tracking_on"}[track], func(t *testing.T) {
			originSessionUnitUnselectedFallback(t, track)
			e, s, d := originSessionUnitFixture(track)
			originSessionUnitSelect(t, e, s, d)
			c := &PayloadOf(s).origin
			c.position = 0x8000000000000010
			ref := s.Transactions.Ref
			e.recordOriginCommitLocked(ref, s)
			if c.stamp != 0 || e.Cluster.replicationOriginProgress[c.id] != 0 || e.Visibility.LastCommittedXID() != 0 {
				t.Fatal("unassigned completion fabricated history/context")
			}
			if err := e.AssignTransactionXIDLocked(ref); err != nil {
				t.Fatal(err)
			}
			xid := e.Visibility.Entries[ref].AssignedXID
			e.recordOriginCommitLocked(ref, s)
			fallback := c.stamp
			if fallback == 0 || e.Cluster.replicationOriginProgress[c.id] != c.position || e.Visibility.Entries[ref].AssignedXID != 0 {
				t.Fatal("assigned completion did not publish once")
			}
			history := e.Visibility.LastCommitTimestamp()
			e.recordOriginCommitLocked(ref, s)
			if e.Visibility.LastCommitTimestamp() != history || c.stamp != fallback {
				t.Fatal("duplicate completion mutated history")
			}
			if track {
				if r := e.Visibility.LookupCommitTimestampLocked(xid); !r.Present || r.Stamp != fallback || r.Origin != uint32(c.id) {
					t.Fatal("history lost physical origin", r)
				}
			} else if e.Visibility.CommitTimestamps != nil {
				t.Fatal("tracking-off allocated timestamp history")
			}
			// Later assigned incarnations retain the first local fallback timestamp, even
			// with tracking disabled; supplied infinities use the exact integer carrier.
			for i, stamp := range []int64{fallback, sqltype.TimestampPositiveInfinity, sqltype.TimestampNegativeInfinity} {
				c.stamp = stamp
				c.position = 1
				s.Transactions.Ref = e.Catalog.Domain.Tx(s.Token(), ids.TxID(i+2), 1, s.Transactions, uint32(i+2))
				next := s.Transactions.Ref
				if err := e.AssignTransactionXIDLocked(next); err != nil {
					t.Fatal(err)
				}
				e.recordOriginCommitLocked(next, s)
				if c.stamp != stamp || e.Cluster.replicationOriginProgress[c.id] != 0x8000000000000010 {
					t.Fatal("completion regressed or replaced configured stamp")
				}
				if track && e.Visibility.LastCommitTimestamp().Stamp != stamp {
					t.Fatal("timestamp integer transport lost infinity")
				}
			}
		})
	}
}
func TestReplicationOriginSessionAbort(t *testing.T) {
	e, s, d := originSessionUnitFixture(true)
	originSessionUnitSelect(t, e, s, d)
	c := &PayloadOf(s).origin
	c.position = 0x20
	ref := s.Transactions.Ref
	e.endSerializable(ref, false)
	if e.Cluster.replicationOriginProgress[c.id] != 0 {
		t.Fatal("no-XID abort advanced")
	}
	if err := e.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	xid := e.Visibility.Entries[ref].AssignedXID
	e.endSerializable(ref, false)
	if e.Cluster.replicationOriginProgress[c.id] != 0x20 || e.Visibility.LookupCommitTimestampLocked(xid).Present || e.Visibility.LastCommittedXID() != 0 || c.position != 0x20 || c.stamp != 0 {
		t.Fatal("abort confused progress/history/context")
	}
	c.position = 0x30
	e.endSerializable(ref, false)
	if e.Cluster.replicationOriginProgress[c.id] != 0x20 {
		t.Fatal("repeated disposal advanced twice")
	}
}
func TestReplicationOriginSessionSubabort(t *testing.T) {
	e, s, d := originSessionUnitFixture(true)
	originSessionUnitSelect(t, e, s, d)
	c := &PayloadOf(s).origin
	c.position = 0x40
	owner := s.Transactions
	ref := owner.Ref
	if err := e.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	owner.Savepoints = []session.SavepointMark{{Serial: 1}, {Serial: 2, Internal: true}}
	if err := e.AssignTopTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	e.abortOriginSubtransactionLocked(owner, 2)
	if e.Cluster.replicationOriginProgress[c.id] != 0 || owner.Savepoints[0].Assigned {
		t.Fatal("top inquiry assigned a read-only child")
	}
	if err := e.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	if !owner.Savepoints[0].Assigned || !owner.Savepoints[1].Assigned {
		t.Fatal("current assignment lost child ancestors")
	}
	owner.TruncateSavepoints(1) // subcommit never publishes, ancestor remains assigned
	e.abortOriginSubtransactionLocked(owner, 1)
	if e.Cluster.replicationOriginProgress[c.id] != 0x40 || owner.Savepoints[0].Assigned || e.Visibility.Entries[ref].AssignedXID == 0 {
		t.Fatal("subabort consumed top identity or retained child assignment")
	}
	c.position = 0x50
	e.abortOriginSubtransactionLocked(owner, 1)
	e.abortOriginSubtransactionLocked(owner, 99)
	if e.Cluster.replicationOriginProgress[c.id] != 0x40 {
		t.Fatal("repeated/atomic restoration fabricated child abort")
	}
	if err := e.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	e.abortOriginSubtransactionLocked(owner, 1)
	if e.Cluster.replicationOriginProgress[c.id] != 0x50 {
		t.Fatal("restarted child did not reassign")
	}
}
func TestReplicationOriginSessionReplacement(t *testing.T) {
	e, peerStore, owner := originReplacementOwnerFixture()
	sAlloc := &sessionAllocation{}
	s := &sAlloc.session
	s.Payload = &sAlloc.payload
	s.SessionToken = e.Catalog.Domain.Session(1)
	d := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 2}, ObjectName: "selected", ObjectRevision: 1}
	originSessionUnitSelect(t, e, s, d)
	peer := e.Cluster.Targets["peer"]
	peer.Cluster = e.Cluster
	pAlloc := &sessionAllocation{}
	ps := &pAlloc.session
	ps.Payload = &pAlloc.payload
	ps.SessionToken = peerStore.Domain.Session(2)
	pending := *d
	pending.ObjectAddress.ObjectID = 3
	e.Cluster.replicationOrigins[3] = replicationOriginReservation{Ref: owner.Ref, Address: pending.Address(), SubscriptionDatabase: peerStore.DatabaseAddress()}
	if err := peer.setupOriginSessionLocked(ps, peerStore.Current(), &pending, 10); err != nil {
		t.Fatal(err)
	}
	pc := &PayloadOf(ps).origin
	pc.position = 0x80
	pc.stamp = 123
	old := *pc
	e.invalidateReplacedOriginSessionsLocked()
	if PayloadOf(s).origin != (replicationOriginSession{}) || e.Cluster.replicationOriginOwners[2] != nil || *pc != old || e.Cluster.replicationOriginOwners[3] != pc {
		t.Fatal("replacement did not eagerly separate selected and retained peer contexts")
	}
	// Ordinary rollback releases allocation claims, not the selected numeric
	// physical slot. All replacement classifiers must agree for that active orphan.
	e.releaseReplicationOriginsLocked(owner.Ref)
	if _, reserved := e.Cluster.replicationOrigins[3]; reserved {
		t.Fatal("orphan still has a reservation")
	}
	ordinary := e.replicationOriginOccupancyLocked(false)
	replacement := e.replicationOriginOccupancyLocked(true)
	if ordinary[0]&(1<<3) != 0 || replacement[0]&(1<<3) == 0 {
		t.Fatal("replacement exclusion changed ordinary numeric reuse")
	}
	colliding := catalog.CloneState(e.Catalog.State)
	colliding.SetDescriptor(pending.Address(), &pending)
	colliding.BindName(catalog.DescriptorNameKey(&pending), pending.Address())
	if err := e.validateReplacementReplicationOriginsLocked(colliding); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("late imported ID collision crossed preflight", err)
	}
	for _, position := range []uint64{0, 0x8000000000000008} {
		e.Cluster.replicationOriginProgress[3] = position
		next := catalog.CloneState(e.Catalog.State)
		imported := make(map[replicationOriginProgressKey]uint64)
		for i := range 10 {
			origin := &catalog.ReplicationOrigin{ObjectAddress: ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: ids.OID(10 + i)}, ObjectName: fmt.Sprintf("import_%d", i), ObjectRevision: 1}
			next.SetDescriptor(origin.Address(), origin)
			next.BindName(catalog.DescriptorNameKey(origin), origin.Address())
			imported[replicationOriginProgressKey{Name: origin.Name()}] = 0
		}
		_, err := e.prepareReplacementOriginProgressLocked(next, imported)
		originSessionUnitCode(t, err, "53400")
		if *pc != old || e.Cluster.replicationOriginProgress[3] != position || e.Cluster.replicationOriginOwners[3] != pc {
			t.Fatal("capacity failure changed active orphan")
		}
		last := ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 19}
		next.UnbindName(catalog.ReplicationOriginNameKey("import_9"))
		next.RemoveDescriptor(last)
		values, err := e.prepareReplacementOriginProgressLocked(next, imported)
		if err != nil || len(values) != 10 {
			t.Fatal("exact orphan/import union", len(values), err)
		}
		if got, present := values[3]; !present || got != position {
			t.Fatal("orphan slot lost or zero elided", got, present)
		}
	}
	delete(e.Cluster.replicationOriginProgress, 3)
	if _, err := e.prepareReplacementOriginProgressLocked(catalog.CloneState(e.Catalog.State), nil); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing active slot was recreated", err)
	}
	// A peer epoch replacement makes that private context ineligible as well.
	peerStore.State.SetEpoch(peerStore.State.Epoch() + 1)
	e.invalidateReplacedOriginSessionsLocked()
	if *pc != (replicationOriginSession{}) || len(e.Cluster.replicationOriginOwners) != 0 {
		t.Fatal("stale peer context survived")
	}
}

func originSessionUnitUnselectedFallback(t *testing.T, track bool) {
	t.Helper()
	e, s, d := originSessionUnitFixture(track)
	c := &PayloadOf(s).origin
	c.stamp = 123 // A prior backend-global value must be replaced by ordinary commit.
	ref := s.Transactions.Ref
	e.recordOriginCommitLocked(ref, s)
	if c.stamp != 123 {
		t.Fatal("unassigned ordinary completion replaced the saved timestamp")
	}
	if err := e.AssignTransactionXIDLocked(ref); err != nil {
		t.Fatal(err)
	}
	xid := e.Visibility.Entries[ref].AssignedXID
	before := time.Now().UnixMicro() - postgresEpochUnixMicros
	e.recordOriginCommitLocked(ref, s)
	after := time.Now().UnixMicro() - postgresEpochUnixMicros
	fallback := c.stamp
	if fallback < before || fallback > after || c.id != 0 || c.token != (ids.SessionToken{}) {
		t.Fatal("ordinary assigned commit lost backend-global fallback", *c, before, after)
	}
	if track {
		if r := e.Visibility.LookupCommitTimestampLocked(xid); !r.Present || r.Stamp != fallback || r.Origin != 0 {
			t.Fatal("ordinary history did not use the saved local stamp", r)
		}
	}
	history := e.Visibility.LastCommitTimestamp()
	e.recordOriginCommitLocked(ref, s)
	if c.stamp != fallback || e.Visibility.LastCommitTimestamp() != history {
		t.Fatal("duplicate/unassigned completion replaced the saved timestamp")
	}
	s.Transactions.Ref = e.Catalog.Domain.Tx(s.Token(), 2, 1, s.Transactions, 2)
	if err := e.AssignTransactionXIDLocked(s.Transactions.Ref); err != nil {
		t.Fatal(err)
	}
	e.endSerializable(s.Transactions.Ref, false)
	if c.stamp != fallback || e.Visibility.LastCommitTimestamp() != history {
		t.Fatal("ordinary assigned abort replaced the saved timestamp")
	}
	originSessionUnitSelect(t, e, s, d)
	if c.stamp != fallback {
		t.Fatal("selection cleared the preceding non-origin fallback")
	}
	e.releaseOriginSessionLocked(s, s.Token())
	if *c != (replicationOriginSession{}) {
		t.Fatal("session reset failed to clear the saved fallback", *c)
	}
	originSessionUnitSelect(t, e, s, d)
	if c.stamp != 0 {
		t.Fatal("reselection resurrected the reset fallback")
	}
	if !track && e.Visibility.CommitTimestamps != nil {
		t.Fatal("tracking-off fallback storage allocated timestamp history")
	}
}
