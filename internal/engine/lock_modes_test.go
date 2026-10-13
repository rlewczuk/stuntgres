package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
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
					e := New()
					op, done := roleOperation(t, e, "postgres")
					defer done()
					roleExecute(t, e, op, "CREATE USER lockreader")
					roleExecute(t, e, op, "CREATE TABLE t(v INT)")
					reader, _ := e.Roles().Lookup("lockreader")
					grant := authorization.ACLGrantee{Role: reader.Ref}
					if public {
						grant = authorization.ACLGrantee{Public: true}
					}
					if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, "t"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: grant, Privileges: right.privilege}}}); err != nil {
						t.Fatal(err)
					}
					roleExecute(t, e, op, "SET SESSION AUTHORIZATION lockreader")
					tx, err := e.Begin(op, TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, statement(t, "LOCK t IN "+mode+" MODE NOWAIT"), 0, nil)
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
					lockEmpty(t, e)
				})
			}
		}
	}
	t.Run("ordinary owner defaults", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		roleExecute(t, e, op, "CREATE USER lockowner")
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION lockowner")
		roleExecute(t, e, op, "CREATE TABLE owned(v INT)")
		tx, err := e.Begin(op, TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range modes {
			roleExecute(t, e, op, "LOCK owned IN "+mode+" MODE NOWAIT")
		}
		if _, err = e.Rollback(op, tx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLockModesOrder(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE first(v INT)")
	sessionSQL(t, a, "CREATE TABLE later(v INT)")
	// Parse on the test goroutine: missing grammar is a normal red assertion,
	// never a panic inside an asynchronous helper.
	parsed := statement(t, "LOCK first, later, missing IN SHARE MODE")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	sessionSQL(t, a, "LOCK TABLE first IN ACCESS EXCLUSIVE MODE")
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
			lockAwait(t, done, context.Canceled)
		}
		transactionEnd(t, b, rb, false)
		transactionEnd(t, a, ra, false)
	}()
	lockPending(t, e, 1)
	first := catalogRef(t, e, e.CatalogRead(), "first")
	later := catalogRef(t, e, e.CatalogRead(), "later")
	e.locks.mu.Lock()
	pending := len(e.locks.entries[lockResource{ObjectRef: first}].pending)
	entry := e.locks.entries[lockResource{ObjectRef: later}]
	e.locks.mu.Unlock()
	if pending != 1 || entry != nil {
		t.Fatal("list was preflighted, sorted, or acquired past the blocked first target")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	joined = true
}
