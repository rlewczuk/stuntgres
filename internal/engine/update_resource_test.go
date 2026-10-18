package engine

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateCoreRowResourceAuthority(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	versions := e.storage[ref.Address].versions
	ta := transactionBegin(t, a)
	tb := transactionBegin(t, b)
	resource := lockResource{ObjectRef: ref, row: versions[0].row}
	other := lockResource{ObjectRef: ref, row: versions[1].row}
	acquire := func(ticket *relationTicket, r lockResource) (*relationRequest, error) {
		return e.locks.startAcquire(context.Background(), ticket, r, noKeyUpdate, relationLockPolicy{})
	}
	if request, err := acquire(a.transactions.ticket, resource); request != nil || err != nil {
		t.Fatal(request, err)
	}
	if request, err := acquire(a.transactions.ticket, resource); request != nil || err != nil {
		t.Fatal("self reentry", request, err)
	}
	if request, err := acquire(b.transactions.ticket, other); request != nil || err != nil {
		t.Fatal("duplicate-valued row conflicted", request, err)
	}
	for _, invalid := range []lockResource{{ObjectRef: ref}, {ObjectRef: New().domain.Ref(ref.Epoch, ref.Address), row: versions[0].row}} {
		if request, err := acquire(b.transactions.ticket, invalid); request != nil || err == nil {
			t.Fatal("invalid resource admitted", request, err)
		}
	}
	copied := *a.transactions.ticket
	if request, err := acquire(&copied, resource); request != nil || !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("copied ticket admitted", request, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := e.locks.startAcquire(ctx, b.transactions.ticket, resource, noKeyUpdate, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal("missing exact queued row request", request, err)
	}
	cancel()
	if err := request.wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.locks.mu.Lock()
	entry := e.locks.entries[resource]
	held := entry.holders[a.transactions.ticket].mode
	pending := len(entry.pending)
	e.locks.mu.Unlock()
	if held != noKeyUpdate || pending != 0 {
		t.Fatal("cancel damaged holder", held, pending)
	}
	request, err = acquire(b.transactions.ticket, resource)
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	transactionEnd(t, a, ta, true)
	if err := request.wait(); err != nil {
		t.Fatal(err)
	}
	transactionEnd(t, b, tb, false)
	lockEmpty(t, e)
}
