package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestRelationLockAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER lockreader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(v INT)")
	ref := enginetest.RoleTableRef(t, e, "t")
	reader, _ := e.Roles().Lookup("lockreader")
	bootstrap := op.RoleState().CurrentUser
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION lockreader")
	enginetest.RoleExecute(t, e, op, "SELECT v FROM t")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1)")
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}) {
		t.Fatalf("ordinary SELECT/INSERT authority accepted AX: %#v %v", diagnostic, err)
	}
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	// Default owner ACL supplies ordinary LOCK privileges.
	withPublicSchemaUnitOwner(t, e, "lockreader", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	})
	tx, err = e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, enginetest.Statement(t, "LOCK TABLE owned IN ACCESS EXCLUSIVE MODE"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	tx, err = e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	enginetest.LockEmpty(t, e)
}
func TestRelationLockJournalBoundaries(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	ta := a.Transactions.Ticket
	tb := b.Transactions.Ticket
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Locks.Acquire(canceled, tb, lock.Resource{ObjectRef: ref}, lock.AccessShare); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	lockState := e.Locks.Inspect()
	if len(lockState.Resources) != 0 {
		t.Fatal("pre-cancel mutated manager")
	}
	if err := e.Locks.Acquire(context.Background(), ta, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Locks.Acquire(ctx, tb, lock.Resource{ObjectRef: ref}, lock.AccessShare) }()
	enginetest.LockPending(t, e, 1)
	// Grant state is installed by end, independently of the sleeping caller.
	e.EndLockOwner(ta)
	lockState = e.Locks.Inspect()
	held := lockState.Resources[lock.Resource{ObjectRef: ref}].Holders[tb].Mode
	if held != lock.AccessShare {
		t.Fatal("grant not journaled before wake", held)
	}
	cancel()
	enginetest.LockAwait(t, done, nil)
	enginetest.TransactionEnd(t, a, ra, false)
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.LockEmpty(t, e)
	// An old callback/fence cannot remove a new incarnation's contribution.
	old := tb
	rb = enginetest.TransactionBegin(t, b)
	tb = b.Transactions.Ticket
	if tb == old {
		t.Fatal("ticket reused")
	}
	if err := e.Locks.Acquire(context.Background(), tb, lock.Resource{ObjectRef: ref}, lock.RowExclusive); err != nil {
		t.Fatal(err)
	}
	e.EndLockOwner(old)
	e.Locks.Fence(e.Catalog.Domain, ref.Epoch)
	lockState = e.Locks.Inspect()
	held = lockState.Resources[lock.Resource{ObjectRef: ref}].Holders[tb].Mode
	if held != lock.RowExclusive {
		t.Fatal("old cleanup removed new hold")
	}
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.LockEmpty(t, e)
}
