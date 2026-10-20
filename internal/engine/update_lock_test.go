package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestUpdateCoreRowWaitRecheck(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "predicate", "cancel", "reset"} {
		t.Run(finish, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			c := sessionStart(t, e, nil)
			sessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
			sessionSQL(t, c, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			if finish == "predicate" {
				sessionSQL(t, a, "UPDATE t SET id=3 WHERE id=1")
				sessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, b, ctx, "UPDATE t SET b=a WHERE id=1", false)
			lockPending(t, e, 1)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.locks.Inspect()
			rowWait := false
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == ref && resource.Row != 0 && len(entry.Pending) == 1 {
					rowWait = true
				}
			}
			if !rowWait {
				t.Fatal("not an actual logical-row wait")
			}
			transactionRows(t, c, "SELECT a FROM t ORDER BY id", 10, 20)
			if finish != "predicate" {
				sessionSQL(t, c, "UPDATE t SET b=20 WHERE id=2")
			}
			// A concurrent matching INSERT is outside B's already frozen candidate set.
			sessionSQL(t, c, "INSERT INTO t VALUES(1,99,0)")
			switch finish {
			case "cancel":
				cancel()
				lockAwait(t, done, context.Canceled)
				transactionEnd(t, a, tx, false)
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ids.ErrCatalogReplaced)
				transactionEnd(t, a, tx, false)
				sessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
				sessionSQL(t, c, "INSERT INTO t VALUES(1,7,0)")
				sessionSQL(t, c, "UPDATE t SET b=a WHERE id=1")
				transactionRows(t, c, "SELECT b FROM t", 7)
				lockEmpty(t, e)
				return
			default:
				transactionEnd(t, a, tx, finish != "rollback")
				lockAwait(t, done, nil)
			}
			switch finish {
			case "commit":
				transactionRows(t, c, "SELECT b FROM t ORDER BY a", 11, 20, 0)
			case "rollback":
				transactionRows(t, c, "SELECT b FROM t ORDER BY a", 10, 20, 0)
			case "predicate":
				transactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 0, 0)
			case "cancel":
				transactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 20, 0)
				sessionSQL(t, c, "UPDATE t SET b=a WHERE a=10")
			}
			lockEmpty(t, e)
		})
	}
}

func TestUpdateCoreImmediateGrantRecheck(t *testing.T) {
	for _, predicateChanged := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_rhs", true: "fresh_predicate"}[predicateChanged], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(id INT,a INT,b INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			op, scope, bound, source := updateBoundSource(t, e, b, context.Background(), "UPDATE t SET b=a WHERE id=1")
			original := source.VersionAt(source.RawIndex(0))
			if predicateChanged {
				sessionSQL(t, a, "UPDATE t SET id=3,a=11 WHERE id=1")
				sessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			} else {
				sessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			}
			// The peer has committed and released the row before acquisition,
			// but this complete executor still starts from its captured old view.
			r, err := e.executeUpdate(op.Ctx, scope, bound, source, nil, nil, "public")
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
			if testExport(original.Values[1]) != int64(10) {
				t.Fatal("snapshot rewritten")
			}
			if predicateChanged {
				transactionRows(t, a, "SELECT b FROM t ORDER BY a", 0, 0)
			} else {
				transactionRows(t, a, "SELECT b FROM t ORDER BY a", 11, 0)
			}
			lockEmpty(t, e)
		})
	}
}

func TestUpdateCoreRowDeadlinesAndExplicitAbort(t *testing.T) {
	for _, setting := range []struct{ q, state, message string }{{"SET lock_timeout='2ms'", "55P03", "canceling statement due to lock timeout"}, {"SET statement_timeout='2ms'", "57014", "canceling statement due to statement timeout"}} {
		t.Run(setting.state, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ta := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE t SET v=2")
			tb := transactionBegin(t, b)
			sessionSQL(t, b, setting.q)
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
			if b.TransactionState() != TxAborted {
				t.Fatal("error did not abort explicit block")
			}
			transactionEnd(t, b, tb, false)
			transactionEnd(t, a, ta, false)
			lockEmpty(t, e)
		})
	}
}
