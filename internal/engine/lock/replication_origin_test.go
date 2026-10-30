package lock

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestReplicationOriginStateNameLocks(t *testing.T) {
	for _, name := range []string{"", "origin", "Origin", " origin "} {
		t.Run(name, func(t *testing.T) {
			a, b, shared := roleLockFixtures(t)
			holder, waiter := a.tx(), b.tx()
			resource := Resource{ObjectRef: shared.Domain().Ref(shared.Epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.ReplicationOriginClassID, Name: name}}
			for _, mutate := range []func(*Resource){
				func(r *Resource) { r.ObjectRef = a.domain.Ref(a.epoch, r.Address) },
				func(r *Resource) { r.ObjectRef = b.domain.Ref(b.epoch, r.Address) },
				func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(r.Epoch, r.Address) },
				func(r *Resource) { r.Epoch++ },
				func(r *Resource) {
					r.Name.Namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
					r.Address = r.Name.Namespace
				},
				func(r *Resource) { r.Name.Signature = "text" },
				func(r *Resource) { r.Address = ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 1} },
				func(r *Resource) { r.RoleTuple = true },
				func(r *Resource) { r.Row = 1 },
				func(r *Resource) { r.Name.ClassID = ids.ParameterClassID; r.Name.Name = "" },
			} {
				bad := resource
				mutate(&bad)
				if request, err := a.m.StartAcquire(context.Background(), holder, bad, AccessExclusive, Policy{}); err == nil || request != nil {
					t.Fatal("invalid shared origin name authority admitted", bad, err)
				}
			}
			if request, err := a.m.StartAcquire(context.Background(), holder, resource, AccessShare, Policy{}); err == nil || request != nil {
				t.Fatal("nonwriter origin name mode admitted", err)
			}
			if a.request(holder, resource, AccessExclusive) != nil {
				t.Fatal("uncontended origin name queued")
			}
			other := resource
			other.Name.Name += "x"
			if b.request(waiter, other, AccessExclusive) != nil {
				t.Fatal("different exact TEXT name serialized")
			}
			holder.MirrorFullXID(42)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pending, err := b.m.StartAcquire(ctx, waiter, resource, AccessExclusive, Policy{})
			if err != nil || pending == nil {
				t.Fatal("peer bypassed shared origin name writer", pending, err)
			}
			pids, err := b.m.BlockingPids(b.domain, waiter.Session().ID())
			if err != nil || len(pids) != 1 || pids[0] != int32(holder.Session().ID()) {
				t.Fatal("missing actual cross-database blocker", pids, err)
			}
			records, err := b.m.PGLocks(b.domain, b.epoch)
			if err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, record := range records {
				if record.Kind == RelationRecord || record.Kind == ObjectRecord || record.Kind == AdvisoryRecord {
					t.Fatal("origin name fabricated physical lock identity", record)
				}
				if record.Waiting && record.Kind == TransactionRecord && record.XID == 42 && record.Owner.Session == waiter.Session().ID() {
					seen = true
				}
			}
			if !seen {
				t.Fatal("origin name lost real blocker XID")
			}
			cancel()
			if err := pending.Wait(); !errors.Is(err, context.Canceled) {
				t.Fatal("origin name cancellation", err)
			}
			// Cancellation removes only the pending contribution, not its blocker.
			retry := b.request(waiter, resource, AccessExclusive)
			if retry == nil {
				t.Fatal("cancellation released another owner's origin name")
			}
			a.m.End(holder)
			if err := b.result(retry); err != nil {
				t.Fatal(err)
			}
			b.m.End(waiter)
			a.empty()
		})
	}
}
