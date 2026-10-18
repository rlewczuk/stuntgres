package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestRelationLockIdentity(t *testing.T) {
	e := New()
	other := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	foreign := sessionStart(t, other, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "CREATE TABLE u(v INT)")
	sessionSQL(t, foreign, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	rf := transactionBegin(t, foreign)
	tref := catalogRef(t, e, e.CatalogRead(), "t")
	uref := catalogRef(t, e, e.CatalogRead(), "u")
	ticket := a.transactions.ticket
	if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: tref}, accessExclusive); err != nil {
		t.Fatal(err)
	}
	if err := e.locks.acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: uref}, accessExclusive); err != nil {
		t.Fatal("distinct relation blocked", err)
	}
	for _, invalid := range []*relationTicket{nil, foreign.transactions.ticket} {
		if err := e.locks.acquire(context.Background(), invalid, lockResource{ObjectRef: tref}, accessShare); err == nil {
			t.Fatal("foreign ticket accepted")
		}
	}
	for _, invalid := range []ids.ObjectRef{{}, catalogRef(t, other, other.CatalogRead(), "t"), tref.Domain().Ref(tref.Epoch+1, tref.Address), tref.Domain().Ref(tref.Epoch, ids.ObjectAddress{ClassID: RelationClassID, ObjectID: tref.Address.ObjectID, SubID: 1})} {
		if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: invalid}, accessShare); err == nil {
			t.Fatal("invalid resource accepted", invalid)
		}
	}
	transactionEnd(t, a, ra, false)
	if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: tref}, accessShare); err == nil {
		t.Fatal("retired ticket accepted")
	}
	transactionEnd(t, b, rb, false)
	transactionEnd(t, foreign, rf, false)
	lockEmpty(t, e)
	lockEmpty(t, other)
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT)")); err != nil {
		t.Fatal(err)
	}
	ra = transactionBegin(t, a)
	newref := catalogRef(t, e, e.CatalogRead(), "t")
	if err := e.locks.acquire(context.Background(), a.transactions.ticket, lockResource{ObjectRef: newref}, accessExclusive); err != nil {
		t.Fatal(err)
	}
	e.locks.fence(tref.Epoch)
	e.endLockOwner(ticket)
	e.locks.mu.Lock()
	held := e.locks.entries[lockResource{ObjectRef: newref}].holders[a.transactions.ticket].mode
	e.locks.mu.Unlock()
	if held != accessExclusive {
		t.Fatal("delayed old cleanup released newer epoch")
	}
	transactionEnd(t, a, ra, false)
	lockEmpty(t, e)
}

type relationFailReader struct{ err error }

func (r relationFailReader) Read([]byte) (int, error) { return 0, r.err }
func TestRelationLockFailedControlPreservesOwner(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	epoch := e.CatalogRead().Epoch()
	readErr := errors.New("reader failure")
	if err := e.Restore(context.Background(), relationFailReader{readErr}); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Restore(ctx, strings.NewReader("CREATE TABLE t(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch {
		t.Fatal("failed control changed epoch")
	}
	done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	lockPending(t, e, 1)
	transactionEnd(t, a, ra, true)
	lockAwait(t, done, nil)
	transactionRows(t, b, "SELECT v FROM t", 1)
	lockEmpty(t, e)
}
