package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRelationLockAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER lockreader")
	roleExecute(t, e, op, "CREATE TABLE t(v INT)")
	ref := roleTableRef(t, e, "t")
	reader, _ := e.Roles().Lookup("lockreader")
	bootstrap := op.RoleState().CurrentUser
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION lockreader")
	roleExecute(t, e, op, "SELECT v FROM t")
	roleExecute(t, e, op, "INSERT INTO t VALUES(1)")
	tx, err := e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}) {
		t.Fatalf("ordinary SELECT/INSERT authority accepted AX: %#v %v", diagnostic, err)
	}
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	// Default owner ACL supplies ordinary LOCK privileges.
	withPublicSchemaUnitOwner(t, e, "lockreader", func() {
		roleExecute(t, e, op, "CREATE TABLE owned(v INT)")
	})
	tx, err = e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "LOCK TABLE owned IN ACCESS EXCLUSIVE MODE"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	tx, err = e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, e)
}
func TestRelationLockJournalBoundaries(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	ta := a.transactions.ticket
	tb := b.transactions.ticket
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.locks.Acquire(canceled, tb, lockResource{ObjectRef: ref}, accessShare); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.locks.mu.Lock()
	if len(e.locks.entries) != 0 {
		t.Fatal("pre-cancel mutated manager")
	}
	e.locks.mu.Unlock()
	if err := e.locks.Acquire(context.Background(), ta, lockResource{ObjectRef: ref}, accessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.locks.Acquire(ctx, tb, lockResource{ObjectRef: ref}, accessShare) }()
	lockPending(t, e, 1)
	// Grant state is installed by end, independently of the sleeping caller.
	e.endLockOwner(ta)
	e.locks.mu.Lock()
	held := e.locks.entries[lockResource{ObjectRef: ref}].holders[tb].Mode
	e.locks.mu.Unlock()
	if held != accessShare {
		t.Fatal("grant not journaled before wake", held)
	}
	cancel()
	lockAwait(t, done, nil)
	transactionEnd(t, a, ra, false)
	transactionEnd(t, b, rb, false)
	lockEmpty(t, e)
	// An old callback/fence cannot remove a new incarnation's contribution.
	old := tb
	rb = transactionBegin(t, b)
	tb = b.transactions.ticket
	if tb == old {
		t.Fatal("ticket reused")
	}
	if err := e.locks.Acquire(context.Background(), tb, lockResource{ObjectRef: ref}, rowExclusive); err != nil {
		t.Fatal(err)
	}
	e.endLockOwner(old)
	e.locks.Fence(ref.Epoch)
	e.locks.mu.Lock()
	held = e.locks.entries[lockResource{ObjectRef: ref}].holders[tb].Mode
	e.locks.mu.Unlock()
	if held != rowExclusive {
		t.Fatal("old cleanup removed new hold")
	}
	transactionEnd(t, b, rb, false)
	lockEmpty(t, e)
}
