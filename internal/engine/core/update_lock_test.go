package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestUpdateCoreRowWaitRecheck(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "predicate", "cancel", "reset"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			c := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			if finish == "predicate" {
				enginetest.SessionSQL(t, a, "UPDATE t SET id=3 WHERE id=1")
				enginetest.SessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "UPDATE t SET b=a WHERE id=1", false)
			enginetest.LockPending(t, e, 1)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.Locks.Inspect()
			rowWait := false
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == ref && resource.Row != 0 && len(entry.Pending) == 1 {
					rowWait = true
				}
			}
			if !rowWait {
				t.Fatal("not an actual logical-row wait")
			}
			enginetest.TransactionRows(t, c, "SELECT a FROM t ORDER BY id", 10, 20)
			if finish != "predicate" {
				enginetest.SessionSQL(t, c, "UPDATE t SET b=20 WHERE id=2")
			}
			// A concurrent matching INSERT is outside B's already frozen candidate set.
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,99,0)")
			switch finish {
			case "cancel":
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.TransactionEnd(t, a, tx, false)
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,7,0)")
				enginetest.SessionSQL(t, c, "UPDATE t SET b=a WHERE id=1")
				enginetest.TransactionRows(t, c, "SELECT b FROM t", 7)
				enginetest.LockEmpty(t, e)
				return
			default:
				enginetest.TransactionEnd(t, a, tx, finish != "rollback")
				enginetest.LockAwait(t, done, nil)
			}
			switch finish {
			case "commit":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 11, 20, 0)
			case "rollback":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 10, 20, 0)
			case "predicate":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 0, 0)
			case "cancel":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 20, 0)
				enginetest.SessionSQL(t, c, "UPDATE t SET b=a WHERE a=10")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestUpdateCoreImmediateGrantRecheck(t *testing.T) {
	for _, predicateChanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_rhs", true: "fresh_predicate"}[predicateChanged], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(id INT,a INT,b INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			op, scope, bound, source := updateBoundSource(t, e, b, context.Background(), "UPDATE t SET b=a WHERE id=1")
			original := source.VersionAt(source.RawIndex(0))
			if predicateChanged {
				enginetest.SessionSQL(t, a, "UPDATE t SET id=3,a=11 WHERE id=1")
				enginetest.SessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			} else {
				enginetest.SessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			}
			// The peer has committed and released the row before acquisition,
			// but this complete executor still starts from its captured old view.
			r, err := core.ExecuteUpdate(op.Ctx, scope, bound, source, nil, nil, "public")
			want := int64(1)
			if predicateChanged {
				want = 0
			}
			if err != nil || r.Count != want {
				scope.Finish(err)
				op.Done()
				t.Fatal(r, err)
			}
			if err = scope.Finish(nil); err != nil {
				op.Done()
				t.Fatal(err)
			}
			op.Done()
			if enginetest.Export(original.Values[1]) != int64(10) {
				t.Fatal("snapshot rewritten")
			}
			if predicateChanged {
				enginetest.TransactionRows(t, a, "SELECT b FROM t ORDER BY a", 0, 0)
			} else {
				enginetest.TransactionRows(t, a, "SELECT b FROM t ORDER BY a", 11, 0)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestUpdateCoreRowDeadlinesAndExplicitAbort(t *testing.T) {
	for _, setting := range []struct{ q, state, message string }{{"SET lock_timeout='2ms'", "55P03", "canceling statement due to lock timeout"}, {"SET statement_timeout='2ms'", "57014", "canceling statement due to statement timeout"}} {
		t.Run(setting.state, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ta := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
			tb := enginetest.TransactionBegin(t, b)
			enginetest.SessionSQL(t, b, setting.q)
			done := waitsCommandAsync(e, b, context.Background(), "UPDATE t SET v=3", false)
			select {
			case err := <-done:
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != setting.state || diagnostic.Message != setting.message || diagnostic.Position != "" {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("SQL deadline did not stop row wait")
			}
			if b.TransactionState() != session.TxAborted {
				t.Fatal("error did not abort explicit block")
			}
			enginetest.TransactionEnd(t, b, tb, false)
			enginetest.TransactionEnd(t, a, ta, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
