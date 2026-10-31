package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/core"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestDeleteCoreRelationLifecycle(t *testing.T) {
	for _, kind := range []string{"prepare", "direct", "cached"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			ast := enginetest.Statement(t, "DELETE FROM t")
			var expected core.Description
			if kind == "cached" {
				op := enginetest.TransactionOperation(t, b)
				var err error
				expected, err = e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
			}
			ar := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan deleteOutcome, 1)
			go func() {
				op, err := b.Acquire(ctx)
				var result core.Result
				if err == nil {
					if kind == "prepare" {
						_, err = e.Describe(op, ast)
					} else {
						scope, startErr := e.StartStatement(op)
						err = startErr
						if err == nil {
							if kind == "cached" {
								scope.Expected = &expected
							}
							result, err = scope.Execute(ast, 0, nil)
							err = scope.Finish(err)
						}
					}
					op.Done()
				}
				done <- deleteOutcome{result, err}
			}()
			enginetest.LockPending(t, e, 1)
			target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: target}]
			queuedRX := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.RowExclusive
			for resource := range lockState.Resources {
				if resource.Row != 0 {
					queuedRX = false
				}
			}
			if !queuedRX {
				t.Fatal("DELETE did not wait on RX relation before view capture")
			}
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			enginetest.TransactionEnd(t, a, ar, true)
			count := int64(1)
			if kind == "prepare" {
				count = 0
			}
			deleteAwait(t, done, count, nil)
			if kind == "prepare" {
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
				enginetest.DeleteCount(t, b, "DELETE FROM t", 1)
			} else {
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("explicit_prepare_retains_RX_only", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
		ref := enginetest.TransactionBegin(t, s)
		op := enginetest.TransactionOperation(t, s)
		description, err := e.Describe(op, enginetest.Statement(t, "DELETE FROM t"))
		op.Done()
		if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || len(s.Transactions.Deletes) != 0 {
			t.Fatal(description, err)
		}
		held := 0
		for resource, entry := range e.Locks.Inspect().Resources {
			if resource.Row != 0 || entry.Holders[s.Transactions.Ticket].Mode != lock.RowExclusive {
				held = -100
				break
			}
			held++
		}
		if held != 1 {
			t.Fatal("semantic Prepare holds rows or lost RX", held)
		}
		enginetest.TransactionEnd(t, s, ref, false)
		enginetest.LockEmpty(t, e)
	})
}
