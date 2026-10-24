package lock

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"stuntgres/internal/engine/ids"
)

func TestTransactionalPrivilegesLockResources(t *testing.T) {
	if size := unsafe.Sizeof(Resource{}); size > 128 {
		t.Fatalf("resource exceeded the incoming inline map-key layout: %d", size)
	}
	f := newLockFixture(t)
	a, b, c := f.tx(), f.tx(), f.tx()
	early := f.relation()
	early.Kind = CatalogACLResource
	late := DefaultACLWriter(f.domain.Ref(f.epoch, ids.ObjectAddress{}), 10, 1)
	wide := DefaultACLWriter(late.ObjectRef, uint64(1)<<32|late.DefaultCreator(), late.DefaultClass)
	if wide == late || wide.DefaultCreator() != uint64(1)<<32|10 {
		t.Fatal("default creator identity was truncated")
	}
	for _, mutate := range []func(*Resource){
		func(r *Resource) { r.ObjectRef = new(ids.Domain).Ref(f.epoch, r.Address) },
		func(r *Resource) { r.Epoch++ },
		func(r *Resource) { r.High, r.Low = 0, 0 },
		func(r *Resource) { r.DefaultClass = 0 },
		func(r *Resource) { r.DefaultClass = 7 },
		func(r *Resource) { r.Address = early.Address },
		func(r *Resource) { r.Row = 1 },
		func(r *Resource) { r.Kind = CatalogACLResource },
	} {
		bad := late
		mutate(&bad)
		if request, err := f.m.StartAcquire(context.Background(), a, bad, AccessExclusive, Policy{}); err == nil || request != nil {
			t.Fatal("invalid default claim admitted", bad, err)
		}
	}
	if request, err := f.m.StartAcquire(context.Background(), a, early, AccessShare, Policy{}); err == nil || request != nil {
		t.Fatal("nonexclusive catalog writer claim admitted")
	}
	f.request(a, early, AccessExclusive)
	mark := f.m.Checkpoint(a)
	f.request(a, late, AccessExclusive)
	other := DefaultACLWriter(late.ObjectRef, late.DefaultCreator()+1, late.DefaultClass)
	if f.request(b, other, AccessExclusive) != nil {
		t.Fatal("independent creator serialized")
	}
	waiting := f.request(b, late, AccessExclusive)
	if waiting == nil {
		t.Fatal("same default writer did not queue")
	}
	a.MirrorFullXID(42)
	pids, err := f.m.BlockingPids(f.domain, b.Session().ID())
	if err != nil || len(pids) != 1 || pids[0] != int32(a.Session().ID()) {
		t.Fatal("claim blocker", pids, err)
	}
	records, err := f.m.PGLocks(f.domain, f.epoch)
	if err != nil {
		t.Fatal(err)
	}
	seenWait := false
	for _, record := range records {
		if record.Kind == RelationRecord || record.Kind == ObjectRecord || record.Kind == AdvisoryRecord {
			t.Fatal("claim fabricated a physical resource", record)
		}
		if record.Waiting && record.Kind == TransactionRecord && record.XID == 42 {
			seenWait = true
		}
	}
	if !seenWait {
		t.Fatal("claim lost actual transaction-completion dependency")
	}
	ordinary := early
	ordinary.Kind = OrdinaryResource
	if f.request(c, ordinary, AccessShare) != nil {
		t.Fatal("catalog writer claim acted as relation AX")
	}
	f.m.Restore(a, mark)
	if err := f.result(waiting); err != nil {
		t.Fatal(err)
	}
	if f.m.Inspect(a).Resources[early].Holders[a].Mode != AccessExclusive {
		t.Fatal("checkpoint lost premark claim")
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := f.m.StartAcquire(ctx, b, early, AccessExclusive, Policy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	cancel()
	if err := f.result(request); !errors.Is(err, context.Canceled) {
		t.Fatal("claim cancellation", err)
	}
	f.m.End(a)
	f.m.End(b)
	f.m.End(c)
	f.empty()
	x, y := f.tx(), f.tx()
	f.request(x, late, AccessExclusive)
	queued := f.request(y, late, AccessExclusive)
	if queued == nil {
		t.Fatal("missing fenced claim waiter")
	}
	f.m.Fence(f.domain, f.epoch+1)
	if err := f.result(queued); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("claim replacement fence", err)
	}
	f.empty()
}

func TestTransactionalPrivilegesLockDomains(t *testing.T) {
	a, b, role := roleLockFixtures(t)
	x, y, retire := a.tx(), b.tx(), b.tx()
	defer a.m.End(x)
	defer b.m.End(y)
	defer b.m.End(retire)
	local := a.relation()
	local.Kind = CatalogACLResource
	peer := local
	peer.ObjectRef = b.domain.Ref(b.epoch, local.Address)
	defaultA := DefaultACLWriter(a.domain.Ref(a.epoch, ids.ObjectAddress{}), 10, 1)
	defaultB := DefaultACLWriter(b.domain.Ref(b.epoch, ids.ObjectAddress{}), 10, 1)
	a.request(x, local, AccessExclusive)
	a.request(x, defaultA, AccessExclusive)
	if b.request(y, peer, AccessExclusive) != nil || b.request(y, defaultB, AccessExclusive) != nil {
		t.Fatal("equal ACL/default keys aliased across databases")
	}
	a.request(x, role, AccessShare)
	b.request(y, role, AccessShare)
	wait := b.request(retire, role, AccessExclusive)
	if wait == nil {
		t.Fatal("shared principal retirement bypassed dependency readers")
	}
	a.m.Fence(a.domain, a.epoch+1)
	for _, resource := range []Resource{peer, defaultB, role} {
		if b.m.Inspect(y).Resources[resource].Holders[y].Mode == 0 {
			t.Fatal("database replacement retired another database's claim", resource)
		}
	}
	select {
	case <-wait.Done():
		t.Fatal("replacement discarded the other database's principal dependency")
	default:
	}
	b.m.End(y)
	if err := b.result(wait); err != nil {
		t.Fatal(err)
	}
	if b.request(retire, defaultB, AccessExclusive) != nil {
		t.Fatal("unrelated database default key did not survive replacement")
	}
	b.m.End(retire)
	a.empty()
}
