package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
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
	row.Row = 1
	ta, tb := a.transactions.ticket, b.transactions.ticket
	acquire := func(resource lockResource, mode relationLockMode) {
		t.Helper()
		if err := e.locks.Acquire(context.Background(), ta, resource, mode); err != nil {
			t.Fatal(err)
		}
	}
	acquire(table, shareUpdateExclusive)
	acquire(table, relationShare)
	acquire(row, keyShare)
	mark := e.locks.Checkpoint(ta)
	acquire(table, accessExclusive)
	acquire(row, rowUpdate)
	next := table
	next.Row = 2
	acquire(next, noKeyUpdate)
	request, err := e.locks.StartAcquire(context.Background(), tb, next, rowUpdate, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	e.locks.Restore(ta, mark)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	lockState := e.locks.Inspect()
	modes := lockState.Resources[table].Holders[ta].Mode
	rowMode := lockState.Resources[row].Holders[ta].Mode
	newMode := lockState.Resources[next].Holders[ta].Mode
	if modes != shareUpdateExclusive|relationShare || rowMode != keyShare || newMode != 0 {
		t.Fatal(modes, rowMode, newMode)
	}
	e.locks.Restore(ta, mark)
	e.endLockOwner(ta)
	e.locks.Restore(ta, mark)
	lockState = e.locks.Inspect(ta)
	_, alive := lockState.Owners[ta.Ref()]
	held := len(lockState.Journals[ta])
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
	withPublicSchemaUnitOwner(t, e, "lockowner", func() {
		roleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	})
	ref := roleTableRef(t, e, "owned")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
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
