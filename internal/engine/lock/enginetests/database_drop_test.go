package lock_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestDatabaseDropDomainRetirement(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	selected, peer, shared := new(ids.Domain), new(ids.Domain), new(ids.Domain)
	manager := lock.New(selected)
	if err := manager.AdmitDomain(peer); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetRoleDomain(shared, 1); err != nil {
		t.Fatal(err)
	}
	transaction := func(domain *ids.Domain, id ids.SessionID) *lock.Ticket {
		ticket, err := manager.Register(domain.Tx(domain.Session(id), ids.TxID(id), 1, &databaseLockOwner{}, 1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { manager.End(ticket) })
		ticket.MirrorFullXID(uint64(id) + 100)
		return ticket
	}
	a, b, p, q := transaction(selected, 1), transaction(selected, 2), transaction(peer, 3), transaction(peer, 4)
	persistent, err := manager.RegisterSession(selected.Session(5))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.End(persistent)
	tuple := func(domain *ids.Domain) lock.MultiTuple {
		return lock.MultiTuple{Resource: lock.Resource{ObjectRef: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17000}), Row: 1}, Version: 1}
	}
	selectedTuple, peerTuple := tuple(selected), tuple(peer)
	for _, entry := range []struct {
		owner *lock.Ticket
		tuple lock.MultiTuple
	}{{a, selectedTuple}, {b, selectedTuple}, {p, peerTuple}, {q, peerTuple}} {
		if err := manager.Acquire(ctx, entry.owner, entry.tuple.Resource, lock.ForKeyShare); err != nil {
			t.Fatal(err)
		}
		if err := manager.PhysicalEvents(entry.owner, []lock.MultiEvent{{Tuple: entry.tuple, Mode: lock.MultiKeyShare}}); err != nil {
			t.Fatal(err)
		}
	}
	selectedID, peerID := manager.NextMultiXact(selected)-1, manager.NextMultiXact(peer)-1
	selectedMembers, err := manager.MultiXactMembers(selected, a, selectedID)
	if err != nil || len(selectedMembers) != 2 {
		t.Fatal("missing genuine selected multixact", selectedMembers, err)
	}
	peerMembers, err := manager.MultiXactMembers(peer, p, peerID)
	if err != nil || len(peerMembers) != 2 {
		t.Fatal("missing genuine peer multixact", peerMembers, err)
	}
	sharedObject := lock.Resource{ObjectRef: shared.Ref(1, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: 17001})}
	if err := manager.Acquire(ctx, a, sharedObject, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	waiting, err := manager.StartAcquire(ctx, p, sharedObject, lock.AccessShare, lock.Policy{})
	if err != nil || waiting == nil {
		t.Fatal("peer did not queue behind real shared owner", err)
	}
	local := lock.Resource{ObjectRef: selected.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 17002})}
	if err := manager.Acquire(ctx, a, local, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.StartAcquire(ctx, b, local, lock.AccessShare, lock.Policy{})
	if err != nil || pending == nil {
		t.Fatal("selected local waiter missing", err)
	}
	frontier := manager.NextMultiXact(peer)
	manager.RetireDomain(selected)
	if err := pending.Wait(); err == nil {
		t.Fatal("selected waiter survived terminal retirement")
	}
	if err := waiting.Wait(); err != nil {
		t.Fatal("peer shared waiter was retired", err)
	}
	if !a.OwnershipEnded() || !b.OwnershipEnded() || !persistent.OwnershipEnded() || p.OwnershipEnded() || q.OwnershipEnded() {
		t.Fatal("terminal retirement crossed database ownership")
	}
	if _, err := manager.MultiXactMembers(selected, nil, selectedID); err == nil {
		t.Fatal("selected multixact history survived")
	}
	retained, err := manager.MultiXactMembers(peer, p, peerID)
	if err != nil || len(retained) != 2 || retained[0].XID != peerMembers[0].XID || manager.NextMultiXact(peer) != frontier {
		t.Fatal("peer multixact history/frontier changed", retained, err)
	}
	if len(selectedMembers) != 2 {
		t.Fatal("retirement mutated detached member capture")
	}
	if err := manager.AdmitDomain(selected); err == nil {
		t.Fatal("terminal domain was readmitted")
	}
	if stale, err := manager.Register(a.Ref()); stale != nil || err == nil {
		t.Fatal("stale transaction registered")
	}
	if stale, err := manager.RegisterSession(selected.Session(6)); stale != nil || err == nil {
		t.Fatal("stale persistent token registered")
	}
	if err := manager.Acquire(ctx, a, local, lock.AccessShare); err == nil {
		t.Fatal("ended ticket resurrected ownership")
	}
	if records, err := manager.PGLocks(peer, 1); err != nil {
		t.Fatal(err)
	} else {
		found := false
		for _, record := range records {
			if record.Shared && record.Owner.Session == p.Session().ID() && record.Object == 17001 {
				found = true
			}
			if record.Owner.Session == a.Session().ID() || record.Owner.Session == b.Session().ID() {
				t.Fatal("retired owner remained in manager inquiry", record)
			}
		}
		if !found {
			t.Fatal("shared RoleDomain hold was removed with selected domain")
		}
	}
}
