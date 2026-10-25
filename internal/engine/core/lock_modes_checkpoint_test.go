package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestLockModesCheckpointContributions(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	defer enginetest.TransactionEnd(t, a, ra, false)
	defer enginetest.TransactionEnd(t, b, rb, false)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	table := lock.Resource{ObjectRef: ref}
	row := table
	row.Row = 1
	ta, tb := a.Transactions.Ticket, b.Transactions.Ticket
	acquire := func(resource lock.Resource, mode lock.Mode) {
		t.Helper()
		if err := e.Locks.Acquire(context.Background(), ta, resource, mode); err != nil {
			t.Fatal(err)
		}
	}
	acquire(table, lock.ShareUpdateExclusive)
	acquire(table, lock.Share)
	acquire(row, lock.ForKeyShare)
	mark := e.Locks.Checkpoint(ta)
	acquire(table, lock.AccessExclusive)
	acquire(row, lock.ForUpdate)
	next := table
	next.Row = 2
	acquire(next, lock.ForNoKeyUpdate)
	request, err := e.Locks.StartAcquire(context.Background(), tb, next, lock.ForUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	e.Locks.Restore(ta, mark)
	if err := request.Wait(); err != nil {
		t.Fatal(err)
	}
	lockState := e.Locks.Inspect()
	modes := lockState.Resources[table].Holders[ta].Mode
	rowMode := lockState.Resources[row].Holders[ta].Mode
	newMode := lockState.Resources[next].Holders[ta].Mode
	if modes != lock.ShareUpdateExclusive|lock.Share || rowMode != lock.ForKeyShare || newMode != 0 {
		t.Fatal(modes, rowMode, newMode)
	}
	e.Locks.Restore(ta, mark)
	e.EndLockOwner(ta)
	e.Locks.Restore(ta, mark)
	lockState = e.Locks.Inspect(ta)
	_, alive := lockState.Owners[ta.Ref()]
	held := len(lockState.Journals[ta])
	if alive || held != 0 {
		t.Fatal("checkpoint resurrected ended ticket")
	}
}

func TestLockModesRevokedOwner(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER lockowner")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION lockowner")
	withPublicSchemaUnitOwner(t, e, "lockowner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	})
	ref := enginetest.RoleTableRef(t, e, "owned")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "LOCK owned IN ACCESS SHARE MODE"), 0, nil)
	lockModesEngineDenied(t, err, "permission denied for table owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	tx, err = e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "LOCK owned")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.LockEmpty(t, e)
}
