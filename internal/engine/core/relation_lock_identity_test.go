package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestRelationLockIdentity(t *testing.T) {
	e := engine.New()
	other := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	foreign := enginetest.StartSession(t, other, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE u(v INT)")
	enginetest.SessionSQL(t, foreign, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	rf := enginetest.TransactionBegin(t, foreign)
	tref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	uref := enginetest.CatalogRef(t, e, e.CatalogRead(), "u")
	ticket := a.Transactions.Ticket
	if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: tref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	if err := e.Locks.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: uref}, lock.AccessExclusive); err != nil {
		t.Fatal("distinct relation blocked", err)
	}
	for _, invalid := range []*lock.Ticket{nil, foreign.Transactions.Ticket} {
		if err := e.Locks.Acquire(context.Background(), invalid, lock.Resource{ObjectRef: tref}, lock.AccessShare); err == nil {
			t.Fatal("foreign ticket accepted")
		}
	}
	for _, invalid := range []ids.ObjectRef{{}, enginetest.CatalogRef(t, other, other.CatalogRead(), "t"), tref.Domain().Ref(tref.Epoch+1, tref.Address), tref.Domain().Ref(tref.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tref.Address.ObjectID, SubID: 1})} {
		if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: invalid}, lock.AccessShare); err == nil {
			t.Fatal("invalid resource accepted", invalid)
		}
	}
	enginetest.TransactionEnd(t, a, ra, false)
	if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: tref}, lock.AccessShare); err == nil {
		t.Fatal("retired ticket accepted")
	}
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.TransactionEnd(t, foreign, rf, false)
	enginetest.LockEmpty(t, e)
	enginetest.LockEmpty(t, other)
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT)")); err != nil {
		t.Fatal(err)
	}
	ra = enginetest.TransactionBegin(t, a)
	newref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: newref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	e.Locks.Fence(e.Catalog.Domain, tref.Epoch)
	e.EndLockOwner(ticket)
	lockState := e.Locks.Inspect()
	held := lockState.Resources[lock.Resource{ObjectRef: newref}].Holders[a.Transactions.Ticket].Mode
	if held != lock.AccessExclusive {
		t.Fatal("delayed old cleanup released newer epoch")
	}
	enginetest.TransactionEnd(t, a, ra, false)
	enginetest.LockEmpty(t, e)
}

type relationFailReader struct{ err error }

func (r relationFailReader) Read([]byte) (int, error) { return 0, r.err }
func TestRelationLockFailedControlPreservesOwner(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	epoch := e.CatalogRead().Epoch()
	readErr := errors.New("reader failure")
	if err := engine.Restore(context.Background(), e, relationFailReader{readErr}); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(ctx, e, strings.NewReader("CREATE TABLE t(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch {
		t.Fatal("failed control changed epoch")
	}
	done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, a, ra, true)
	enginetest.LockAwait(t, done, nil)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
	enginetest.LockEmpty(t, e)
}
