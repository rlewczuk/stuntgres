package engine

import (
	"context"
	"testing"
)

func TestLockModesCheckpointContributions(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	defer transactionEnd(t, a, ra, false)
	defer transactionEnd(t, b, rb, false)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	table := lockResource{ObjectRef: ref}
	row := table
	row.row = 1
	ta, tb := a.transactions.ticket, b.transactions.ticket
	acquire := func(resource lockResource, mode relationLockMode) {
		t.Helper()
		if err := e.locks.acquire(context.Background(), ta, resource, mode); err != nil {
			t.Fatal(err)
		}
	}
	acquire(table, shareUpdateExclusive)
	acquire(table, relationShare)
	acquire(row, keyShare)
	mark := e.locks.checkpoint(ta)
	acquire(table, accessExclusive)
	acquire(row, rowUpdate)
	next := table
	next.row = 2
	acquire(next, noKeyUpdate)
	request, err := e.locks.startAcquire(context.Background(), tb, next, rowUpdate, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	e.locks.restore(ta, mark)
	if err := request.wait(); err != nil {
		t.Fatal(err)
	}
	e.locks.mu.Lock()
	modes := e.locks.entries[table].holders[ta].mode
	rowMode := e.locks.entries[row].holders[ta].mode
	newMode := e.locks.entries[next].holders[ta].mode
	e.locks.mu.Unlock()
	if modes != shareUpdateExclusive|relationShare || rowMode != keyShare || newMode != 0 {
		t.Fatal(modes, rowMode, newMode)
	}
	e.locks.restore(ta, mark)
	e.locks.end(ta)
	e.locks.restore(ta, mark)
	e.locks.mu.Lock()
	_, alive := e.locks.owners[ta.ref]
	held := len(ta.resources)
	e.locks.mu.Unlock()
	if alive || held != 0 {
		t.Fatal("checkpoint resurrected ended ticket")
	}
}

func TestLockModesRevokedOwner(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER lockowner")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION lockowner")
	roleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	ref := roleTableRef(t, e, "owned")
	if err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "LOCK owned IN ACCESS SHARE MODE"), 0, nil)
	lockModesEngineDenied(t, err, "permission denied for table owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	tx, err = e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "LOCK owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, e)
}
