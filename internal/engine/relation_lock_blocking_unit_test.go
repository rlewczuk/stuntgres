package engine

import (
	"context"
	"errors"
	"testing"
)

func TestBlockingPidsLifecycle(t *testing.T) {
	for _, fence := range []bool{false, true} {
		f := newLockFixture(t)
		at, bt := f.tx(), f.tx()
		aid, bid := at.Session().ID(), bt.Session().ID()
		resource := f.relation()
		if err := f.m.Acquire(context.Background(), at, resource, accessExclusive); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r, err := f.m.StartAcquire(ctx, bt, resource, accessExclusive, relationLockPolicy{})
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
			f.m.Fence(bt.Ref().Epoch() + 1)
		} else {
			cancel()
		}
		err = f.result(r)
		if fence && !errors.Is(err, ErrCatalogReplaced) || !fence && !errors.Is(err, context.Canceled) {
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
