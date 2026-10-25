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

func TestLockModesPrivileges(t *testing.T) {
	modes := []string{"ACCESS SHARE", "ROW SHARE", "ROW EXCLUSIVE", "SHARE UPDATE EXCLUSIVE", "SHARE", "SHARE ROW EXCLUSIVE", "EXCLUSIVE", "ACCESS EXCLUSIVE"}
	for _, right := range []struct {
		name      string
		privilege authorization.Privileges
		allowed   int
	}{
		{"select", authorization.SelectPrivilege, 1}, {"insert", authorization.InsertPrivilege, 3}, {"update", authorization.UpdatePrivilege, 8}, {"delete", authorization.DeletePrivilege, 8}, {"maintain", authorization.MaintainPrivilege, 8}, {"truncate", authorization.TruncatePrivilege, 8}, {"none", 0, 0},
	} {
		for _, public := range []bool{false, true} {
			for i, mode := range modes {
				t.Run(right.name+mode+map[bool]string{false: "role", true: "public"}[public], func(t *testing.T) {
					e := engine.New()
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					enginetest.RoleExecute(t, e, op, "CREATE USER lockreader")
					enginetest.RoleExecute(t, e, op, "CREATE TABLE t(v INT)")
					reader, _ := e.Roles().Lookup("lockreader")
					grant := authorization.ACLGrantee{Role: reader.Ref}
					if public {
						grant = authorization.ACLGrantee{Public: true}
					}
					if err := enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, "t"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: grant, Privileges: right.privilege}}}); err != nil {
						t.Fatal(err)
					}
					enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION lockreader")
					tx, err := e.Begin(op, session.TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, enginetest.Statement(t, "LOCK t IN "+mode+" MODE NOWAIT"), 0, nil)
					if i < right.allowed {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						var d *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
						if !errors.As(err, &d) || *d != want {
							t.Fatalf("got %#v / %v want %#v", d, err, want)
						}
					}
					if _, err := e.Rollback(op, tx); err != nil {
						t.Fatal(err)
					}
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
	t.Run("ordinary owner defaults", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "CREATE USER lockowner")
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION lockowner")
		withPublicSchemaUnitOwner(t, e, "lockowner", func() {
			enginetest.RoleExecute(t, e, op, "CREATE TABLE owned(v INT)")
		})
		tx, err := e.Begin(op, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range modes {
			enginetest.RoleExecute(t, e, op, "LOCK owned IN "+mode+" MODE NOWAIT")
		}
		if _, err = e.Rollback(op, tx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLockModesOrder(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE first(v INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE later(v INT)")
	// Parse on the test goroutine: missing grammar is a normal red assertion,
	// never a panic inside an asynchronous helper.
	parsed := enginetest.Statement(t, "LOCK first, later, missing IN SHARE MODE")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "LOCK TABLE first IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		op, err := b.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(op, parsed, 0, nil)
			op.Done()
		}
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			enginetest.LockAwait(t, done, context.Canceled)
		}
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.TransactionEnd(t, a, ra, false)
	}()
	enginetest.LockPending(t, e, 1)
	first := enginetest.CatalogRef(t, e, e.CatalogRead(), "first")
	later := enginetest.CatalogRef(t, e, e.CatalogRead(), "later")
	lockState := e.Locks.Inspect()
	pending := len(lockState.Resources[lock.Resource{ObjectRef: first}].Pending)
	entry := lockState.Resources[lock.Resource{ObjectRef: later}]
	if pending != 1 || entry != nil {
		t.Fatal("list was preflighted, sorted, or acquired past the blocked first target")
	}
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	joined = true
}
