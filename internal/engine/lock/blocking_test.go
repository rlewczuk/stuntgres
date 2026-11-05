package lock

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestBlockingPidsLifecycle(t *testing.T) {
	for _, fence := range []bool{false, true} {
		f := newLockFixture(t)
		at, bt := f.tx(), f.tx()
		aid, bid := at.Session().ID(), bt.Session().ID()
		resource := f.relation()
		if err := f.m.Acquire(context.Background(), at, resource, AccessExclusive); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r, err := f.m.StartAcquire(ctx, bt, resource, AccessExclusive, Policy{})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		f.blocking(bid, int32(aid))
		// A pending pointer without current journal ownership is stale, not
		// an alternate source of wait state. Inquiry must not settle or edit it.
		f.m.mu.Lock()
		delete(bt.resources, resource)
		f.m.mu.Unlock()
		f.blocking(bid)
		f.m.mu.Lock()
		bt.resources[resource] = struct{}{}
		unchanged := !r.terminal && len(r.entry.pending) == 1 && r.entry.pending[0] == r
		f.m.mu.Unlock()
		if !unchanged {
			t.Fatal("read-only inquiry mutated queue")
		}
		if fence {
			f.m.Fence(f.domain, bt.Ref().Epoch()+1)
		} else {
			cancel()
		}
		err = f.result(r)
		if fence && !errors.Is(err, ids.ErrCatalogReplaced) || !fence && !errors.Is(err, context.Canceled) {
			t.Fatal("wrong wait terminal", err)
		}
		f.blocking(bid)
		// A saved terminal request never becomes an observable wait again.
		f.m.mu.Lock()
		entry := f.m.entries[resource]
		if entry != nil {
			entry.pending = append(entry.pending, r)
		}
		f.m.mu.Unlock()
		f.blocking(bid)
	}
}

type startupPrivateOwner struct{ ids.SessionOwnerMarker }

func TestBlockingPidsStartupAuthority(t *testing.T) {
	t.Run("journal-and-private", func(t *testing.T) {
		a, b, shared := roleLockFixtures(t)
		holder := a.tx()
		object := Resource{ObjectRef: shared.Domain().Ref(shared.Epoch, ids.ObjectAddress{ClassID: ids.DatabaseClassID, ObjectID: 17000})}
		a.request(holder, object, AccessExclusive)
		startup := b.advisory(b.session())
		pending := b.request(startup, object, RowExclusive)
		if pending == nil {
			t.Fatal("missing genuine queued startup")
		}
		a.blocking(startup.Session().ID(), int32(holder.Session().ID()))
		a.m.mu.Lock()
		delete(startup.resources, object)
		a.m.mu.Unlock()
		a.blocking(startup.Session().ID())
		a.m.mu.Lock()
		startup.resources[object] = struct{}{}
		pending.mode = AccessShare
		a.m.mu.Unlock()
		a.blocking(startup.Session().ID())
		a.m.mu.Lock()
		pending.mode = RowExclusive
		unchanged := !pending.terminal && pending.entry == a.m.entries[object] && len(pending.entry.pending) == 1
		a.m.mu.Unlock()
		if !unchanged {
			t.Fatal("startup inquiry changed the authoritative request")
		}
		a.blocking(startup.Session().ID(), int32(holder.Session().ID()))
		private := b.advisory(b.domain.PrivateSession(&startupPrivateOwner{}))
		if b.request(private, object, RowExclusive) == nil {
			t.Fatal("missing private request for visibility boundary")
		}
		a.blocking(0)
		b.blocking(0)
	})
	t.Run("foreign-advisory", func(t *testing.T) {
		a, b, _ := roleLockFixtures(t)
		holder, waiter := b.advisoryParticipant(), b.advisoryParticipant()
		key := Resource{Database: b.domain, Kind: AdvisoryBigintResource, Low: 17}
		b.advisoryGrant(holder, key, AdvisoryExclusive)
		pending, err := b.advisoryAcquire(waiter, key, AdvisoryExclusive, false)
		if err != nil || pending == nil {
			t.Fatal("missing local advisory wait", pending, err)
		}
		b.blocking(waiter.token.ID(), int32(holder.token.ID()))
		a.blocking(waiter.token.ID())
	})
	t.Run("foreign-local", func(t *testing.T) {
		a, b, _ := roleLockFixtures(t)
		holder, waiter := b.tx(), b.tx()
		object := b.relation()
		b.request(holder, object, AccessExclusive)
		if b.request(waiter, object, AccessShare) == nil {
			t.Fatal("missing local relation wait")
		}
		b.blocking(waiter.Session().ID(), int32(holder.Session().ID()))
		a.blocking(waiter.Session().ID())
	})
}
