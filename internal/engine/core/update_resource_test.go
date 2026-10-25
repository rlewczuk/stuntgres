package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestUpdateCoreRowResourceAuthority(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	versions := e.Storage.Relations[ref.Address].Versions
	ta := enginetest.TransactionBegin(t, a)
	tb := enginetest.TransactionBegin(t, b)
	resource := lock.Resource{ObjectRef: ref, Row: versions[0].Row}
	other := lock.Resource{ObjectRef: ref, Row: versions[1].Row}
	acquire := func(ticket *lock.Ticket, r lock.Resource) (*lock.Request, error) {
		return e.Locks.StartAcquire(context.Background(), ticket, r, lock.ForNoKeyUpdate, lock.Policy{})
	}
	if request, err := acquire(a.Transactions.Ticket, resource); request != nil || err != nil {
		t.Fatal(request, err)
	}
	if request, err := acquire(a.Transactions.Ticket, resource); request != nil || err != nil {
		t.Fatal("self reentry", request, err)
	}
	if request, err := acquire(b.Transactions.Ticket, other); request != nil || err != nil {
		t.Fatal("duplicate-valued row conflicted", request, err)
	}
	for _, invalid := range []lock.Resource{{ObjectRef: ref}, {ObjectRef: engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address), Row: versions[0].Row}} {
		if request, err := acquire(b.Transactions.Ticket, invalid); request != nil || err == nil {
			t.Fatal("invalid resource admitted", request, err)
		}
	}
	copied := *a.Transactions.Ticket
	if request, err := acquire(&copied, resource); request != nil || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("copied ticket admitted", request, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("missing exact queued row request", request, err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[resource]
	held := entry.Holders[a.Transactions.Ticket].Mode
	pending := len(entry.Pending)
	if held != lock.ForNoKeyUpdate || pending != 0 {
		t.Fatal("cancel damaged holder", held, pending)
	}
	request, err = acquire(b.Transactions.Ticket, resource)
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	enginetest.TransactionEnd(t, a, ta, true)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, b, tb, false)
	enginetest.LockEmpty(t, e)
}
