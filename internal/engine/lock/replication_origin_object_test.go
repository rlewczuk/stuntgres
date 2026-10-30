package lock

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestReplicationOriginStateObjectLocks(t *testing.T) {
	for _, class := range []ids.OID{ids.ReplicationOriginClassID, ids.SubscriptionClassID} {
		t.Run(map[ids.OID]string{ids.ReplicationOriginClassID: "origin", ids.SubscriptionClassID: "subscription"}[class], func(t *testing.T) {
			a, b, shared := roleLockFixtures(t)
			holder, waiter := a.tx(), b.tx()
			resource := Resource{ObjectRef: shared.Domain().Ref(shared.Epoch, ids.ObjectAddress{ClassID: class, ObjectID: 1})}
			for _, mutate := range []func(*Resource){
				func(r *Resource) { r.ObjectRef = a.domain.Ref(a.epoch, r.Address) },
				func(r *Resource) { r.ObjectRef = b.domain.Ref(b.epoch, r.Address) },
				func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(r.Epoch, r.Address) },
				func(r *Resource) { r.Epoch++ },
				func(r *Resource) { r.Address.ObjectID = 0 },
				func(r *Resource) { r.Address.SubID = 1 },
				func(r *Resource) { r.RoleTuple = true },
				func(r *Resource) { r.Row = 1 },
			} {
				bad := resource
				mutate(&bad)
				if request, err := a.m.StartAcquire(t.Context(), holder, bad, AccessExclusive, Policy{}); err == nil || request != nil {
					t.Fatal("forged shared object authority admitted", bad, err)
				}
			}
			if class == ids.ReplicationOriginClassID {
				bad := resource
				bad.Address.ObjectID = 65535
				if request, err := a.m.StartAcquire(t.Context(), holder, bad, AccessShare, Policy{}); err == nil || request != nil {
					t.Fatal("reserved origin ID admitted", err)
				}
			}
			for _, mode := range []Mode{RowExclusive, TypeExclusive, ShareUpdateExclusive, ForUpdate} {
				if request, err := a.m.StartAcquire(t.Context(), holder, resource, mode, Policy{}); err == nil || request != nil {
					t.Fatal("relation/type mode admitted for shared object", mode, err)
				}
			}
			if a.request(holder, resource, AccessShare) != nil || b.request(waiter, resource, AccessShare) != nil {
				t.Fatal("shared dependency reads conflicted")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pending, err := b.m.StartAcquire(ctx, waiter, resource, AccessExclusive, Policy{})
			if err != nil || pending == nil {
				t.Fatal("peer bypassed actual shared object", pending, err)
			}
			pids, err := b.m.BlockingPids(b.domain, waiter.Session().ID())
			if err != nil || len(pids) != 1 || pids[0] != int32(holder.Session().ID()) {
				t.Fatal("wrong shared object blocker", pids, err)
			}
			for _, fixture := range []*lockFixture{a, b} {
				records, err := fixture.m.PGLocks(fixture.domain, fixture.epoch)
				if err != nil {
					t.Fatal(err)
				}
				grants, waits := 0, 0
				for _, record := range records {
					if record.Object != uint32(resource.Address.ObjectID) || record.Class != uint32(class) {
						continue
					}
					if record.Kind != ObjectRecord || !record.Shared {
						t.Fatal("shared catalog object projected as relation/local tag", record)
					}
					if record.Waiting {
						waits++
					} else {
						grants++
					}
				}
				if grants != 2 || waits != 1 {
					t.Fatal("missing actual shared object grants/wait", grants, waits)
				}
			}
			cancel()
			if err := pending.Wait(); !errors.Is(err, context.Canceled) || !b.m.Holds(waiter, resource, AccessShare) || b.m.Holds(waiter, resource, AccessExclusive) {
				t.Fatal("canceled upgrade changed earlier shared grant", err)
			}
			b.m.End(waiter)
			mark := a.m.Checkpoint(holder)
			if a.request(holder, resource, AccessExclusive) != nil {
				t.Fatal("uncontended object upgrade queued")
			}
			a.m.Restore(holder, mark)
			if !a.m.Holds(holder, resource, AccessShare) || a.m.Holds(holder, resource, AccessExclusive) {
				t.Fatal("object savepoint lost original grant")
			}
			a.m.End(holder)
			a.empty()
		})
	}
}
