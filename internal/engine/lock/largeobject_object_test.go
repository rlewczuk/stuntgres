package lock

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestLargeObjectObjectLocks(t *testing.T) {
	t.Run("invalid_resources_and_modes", func(t *testing.T) {
		a, b, shared := roleLockFixtures(t)
		owner := a.tx()
		resource := Resource{ObjectRef: a.domain.Ref(a.epoch, ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 921851})}
		for _, mutate := range []func(*Resource){
			func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(r.Epoch, r.Address) },
			func(r *Resource) { r.ObjectRef = b.domain.Ref(b.epoch, r.Address) },
			func(r *Resource) { r.ObjectRef = shared.Domain().Ref(shared.Epoch, r.Address) },
			func(r *Resource) { r.ObjectRef = ids.ObjectRef{Address: r.Address, Epoch: r.Epoch} },
			func(r *Resource) { r.Epoch-- },
			func(r *Resource) { r.Epoch++ },
			func(r *Resource) { r.Address.ObjectID = 0 },
			func(r *Resource) { r.Address.SubID = 1 },
			func(r *Resource) { r.Address.SubID = -1 },
			func(r *Resource) { r.RoleTuple = true },
			func(r *Resource) { r.Row = 1 },
			func(r *Resource) { r.key = &UniqueKey{} },
			func(r *Resource) { r.Database = a.domain },
			func(r *Resource) { r.High = 1 },
			func(r *Resource) { r.Low = 1 },
			func(r *Resource) { r.DefaultClass = 1 },
			func(r *Resource) { r.Name = ids.NameKey{ClassID: ids.LargeObjectClassID, Name: "921851"} },
			func(r *Resource) { r.Address.ClassID = 999999 },
		} {
			bad := resource
			mutate(&bad)
			if request, err := a.m.StartAcquire(t.Context(), owner, bad, AccessExclusive, Policy{}); err == nil || request != nil {
				t.Fatal("invalid large object resource admitted", bad, request, err)
			}
			if len(a.m.Inspect().Resources) != 0 {
				t.Fatal("invalid resource left a grant or queued request", bad)
			}
		}
		for _, mode := range []Mode{0, RowShare, RowExclusive, Share, ShareUpdateExclusive, ShareRowExclusive, Exclusive, TypeExclusive, ForUpdate, ForNoKeyUpdate, ForKeyShare, ForShare, UniqueClaim, TransactionCompletion, AdvisoryShare, AccessShare | AccessExclusive} {
			if request, err := a.m.StartAcquire(t.Context(), owner, resource, mode, Policy{}); err == nil || request != nil {
				t.Fatal("non-object mode admitted for large object", mode, request, err)
			}
			if len(a.m.Inspect().Resources) != 0 {
				t.Fatal("invalid mode left a grant or queued request", mode)
			}
		}
		a.m.End(owner)
		a.empty()
	})

	t.Run("local_queue_capture_cancel_and_checkpoint", func(t *testing.T) {
		a, b, _ := roleLockFixtures(t)
		holder, waiter, peer := a.tx(), a.tx(), b.tx()
		resource := Resource{ObjectRef: a.domain.Ref(a.epoch, ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 921851})}
		peerResource := Resource{ObjectRef: b.domain.Ref(b.epoch, resource.Address)}
		if a.request(holder, resource, AccessShare) != nil || a.request(waiter, resource, AccessShare) != nil || b.request(peer, peerResource, AccessExclusive) != nil {
			t.Fatal("local reads or independent peer object conflicted")
		}
		// Neither same-OID relation authority nor an ACL writer aliases the
		// actual ordinary metadata object resource.
		relation := resource
		relation.Address.ClassID = ids.RelationClassID
		acl := resource
		acl.Kind = CatalogACLResource
		if a.request(waiter, relation, AccessExclusive) != nil || a.request(waiter, acl, AccessExclusive) != nil {
			t.Fatal("large object lock aliased relation or catalog ACL authority")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pending, err := a.m.StartAcquire(ctx, waiter, resource, AccessExclusive, Policy{})
		if err != nil || pending == nil {
			t.Fatal("large object upgrade bypassed genuine peer holder", pending, err)
		}
		pids, err := a.m.BlockingPids(a.domain, waiter.Session().ID())
		if err != nil || len(pids) != 1 || pids[0] != int32(holder.Session().ID()) {
			t.Fatal("wrong large object blocker", pids, err)
		}
		records, err := a.m.PGLocks(a.domain, a.epoch)
		if err != nil {
			t.Fatal(err)
		}
		holderOwner := RecordOwner{Session: holder.Session().ID(), Local: holder.Ref().ID()}
		waiterOwner := RecordOwner{Session: waiter.Session().ID(), Local: waiter.Ref().ID()}
		grants, waits, relations := 0, 0, 0
		for _, record := range records {
			if record.Kind == RelationRecord && record.Object == 921851 {
				if record.Owner != waiterOwner || record.Mode != AccessExclusive || record.Waiting {
					t.Fatal("same-OID relation changed its independent grant", record)
				}
				relations++
			}
			if record.Kind != ObjectRecord {
				continue
			}
			if record.Class != 2613 || record.Object != 921851 || record.Subobject != 0 || record.Shared || record.XID != 0 || record.Target != (RecordOwner{}) {
				t.Fatal("large object capture changed public identity or fabricated ACL object", record)
			}
			if record.Waiting {
				if record.Owner != waiterOwner || record.Mode != AccessExclusive || record.WaitStart.IsZero() {
					t.Fatal("large object wait lost actual owner/mode/time", record)
				}
				waits++
			} else {
				if record.Owner != holderOwner && record.Owner != waiterOwner || record.Mode != AccessShare || !record.WaitStart.IsZero() {
					t.Fatal("large object grant lost actual owner/mode", record)
				}
				grants++
			}
		}
		if grants != 2 || waits != 1 || relations != 1 || resource.Address.ClassID != 2995 || !a.m.Holds(holder, resource, AccessShare) {
			t.Fatal("missing canonical grants/wait or distinct relation", grants, waits, relations, resource)
		}
		peerRecords, err := b.m.PGLocks(b.domain, b.epoch)
		if err != nil {
			t.Fatal(err)
		}
		peerObjects := 0
		for _, record := range peerRecords {
			if record.Kind != ObjectRecord {
				continue
			}
			want := Record{Kind: ObjectRecord, Owner: RecordOwner{Session: peer.Session().ID(), Local: peer.Ref().ID()}, Mode: AccessExclusive, Class: 2613, Object: 921851}
			if record != want {
				t.Fatal("local large object leaked across databases", record, want)
			}
			peerObjects++
		}
		if peerObjects != 1 {
			t.Fatal("peer large object capture", peerObjects)
		}
		cancel()
		if err := pending.Wait(); !errors.Is(err, context.Canceled) || !a.m.Holds(waiter, resource, AccessShare) || a.m.Holds(waiter, resource, AccessExclusive) {
			t.Fatal("canceled upgrade changed prior grant", err)
		}
		if entry := a.m.Inspect().Resources[resource]; entry == nil || len(entry.Pending) != 0 {
			t.Fatal("canceled large object request remained queued", entry)
		}
		mark := a.m.Checkpoint(waiter)
		upgrade := a.request(waiter, resource, AccessExclusive)
		if upgrade == nil {
			t.Fatal("second upgrade bypassed holder")
		}
		a.m.End(holder)
		if err := upgrade.Wait(); err != nil || !a.m.Holds(waiter, resource, AccessExclusive) {
			t.Fatal("holder release did not grant queued object upgrade", err)
		}
		later := resource
		later.Address.ObjectID++
		if a.request(waiter, later, AccessExclusive) != nil {
			t.Fatal("uncontended later object queued")
		}
		a.m.Restore(waiter, mark)
		if !a.m.Holds(waiter, resource, AccessShare) || a.m.Holds(waiter, resource, AccessExclusive) || a.m.Holds(waiter, later, AccessExclusive) ||
			!a.m.Holds(waiter, acl, AccessExclusive) || !a.m.Holds(waiter, relation, AccessExclusive) || !b.m.Holds(peer, peerResource, AccessExclusive) {
			t.Fatal("checkpoint changed original or unrelated resource contributions")
		}
		a.m.End(waiter)
		b.m.End(peer)
		a.empty()
	})

	t.Run("epoch_fence", func(t *testing.T) {
		a, b, _ := roleLockFixtures(t)
		holder, waiter, peer := a.tx(), a.tx(), b.tx()
		resource := Resource{ObjectRef: a.domain.Ref(a.epoch, ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 921851})}
		peerResource := Resource{ObjectRef: b.domain.Ref(b.epoch, resource.Address)}
		a.request(holder, resource, AccessExclusive)
		b.request(peer, peerResource, AccessExclusive)
		pending := a.request(waiter, resource, AccessExclusive)
		if pending == nil {
			t.Fatal("fence witness requires a real object wait")
		}
		a.m.Fence(a.domain, a.epoch+1)
		if err := pending.Wait(); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("epoch fence did not terminate old object wait", err)
		}
		if request, err := a.m.StartAcquire(t.Context(), holder, resource, AccessShare, Policy{}); !errors.Is(err, ids.ErrCatalogReplaced) || request != nil {
			t.Fatal("retired object owner admitted", request, err)
		}
		if !b.m.Holds(peer, peerResource, AccessExclusive) {
			t.Fatal("local epoch fence retired peer object")
		}
		a.epoch++
		current := a.tx()
		resource.ObjectRef = a.domain.Ref(a.epoch, resource.Address)
		if a.request(current, resource, AccessExclusive) != nil {
			t.Fatal("fresh incarnation inherited old object claims")
		}
		a.m.End(current)
		b.m.End(peer)
		a.empty()
	})
}
