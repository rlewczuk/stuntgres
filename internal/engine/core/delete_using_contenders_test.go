package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestDeleteUsingQueuedContenderRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "described"}[prepared], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(lo INT,hi INT,n INT)", "INSERT INTO t VALUES(1,10),(9,5)", "INSERT INTO s VALUES(0,10,101),(0,30,202),(11,30,303)"} {
				enginetest.SessionSQL(t, peer, q)
			}
			ast := enginetest.Statement(t, "DELETE FROM t x USING s WHERE x.id=1 AND x.v>=s.lo AND x.v<=s.hi RETURNING old.v,s.n,s.lo,s.hi")
			var expected *engine.StatementDescription
			if prepared {
				op := enginetest.TransactionOperation(t, writer)
				description, err := e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &description
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			original := e.Storage.Relations[ref.Address].Versions[0]
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "UPDATE t SET v=20 WHERE id=1")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var finishedScope *engine.StatementScope
			done := make(chan deleteOutcome, 1)
			go func() {
				defer close(done)
				var result engine.Result
				op, err := writer.Acquire(ctx)
				if err == nil {
					defer op.Done()
					finishedScope, err = e.StartStatement(op)
					if err == nil {
						defer func() {
							if !finishedScope.Finished {
								finishedScope.Finish(errors.New("abandoned DELETE USING contender"))
							}
						}()
						finishedScope.Expected = expected
						result, err = finishedScope.Execute(ast, 0, nil)
						err = finishedScope.Finish(err)
					}
				}
				done <- deleteOutcome{result: result, err: err}
			}()
			await := deleteUsingJoin(t, cancel, done)
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: original.Row}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ForUpdate && entry.Holders[holder.Transactions.Ticket].Mode == lock.ForNoKeyUpdate
			if !queued {
				t.Fatal("not an actual DELETE rowUpdate queue")
			}
			// Source edits and new pairs cannot replace the captured occurrences.
			enginetest.SessionSQL(t, peer, "UPDATE s SET n=999")
			enginetest.SessionSQL(t, peer, "INSERT INTO s VALUES(0,30,404)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
			enginetest.SessionSQL(t, peer, "UPDATE t SET id=1 WHERE id=9")
			enginetest.TransactionEnd(t, holder, tx, true)
			got := await()
			var rows [][]driver.Value
			if got.err == nil && got.result.Rows != nil {
				rows = enginetest.Tuples(got.result)
				got.result.Rows.Close()
			}
			want := [][]driver.Value{{int64(20), int64(202), int64(0), int64(30)}}
			if got.err != nil || got.result.Count != 1 || !reflect.DeepEqual(rows, want) {
				t.Fatal("first recheck rejected/later pair must remain eligible", got, want)
			}
			if finishedScope == nil || len(finishedScope.QuerySources) != 0 {
				t.Fatal("admission leaked")
			}
			if enginetest.Export(original.Values[1]) != int64(10) {
				t.Fatal("captured predecessor mutated")
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 5, 30)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteUsingContentionOutcomes(t *testing.T) {
	for _, phase := range []string{"rollback", "delete", "move_key", "repeatable_update", "repeatable_delete", "cancel_row", "cancel_source"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1)"} {
				enginetest.SessionSQL(t, peer, q)
			}
			var writerTx ids.TxRef
			if phase == "repeatable_update" || phase == "repeatable_delete" {
				enginetest.SessionSQL(t, writer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				writerTx = writer.Transactions.Ref
				enginetest.TransactionRows(t, writer, "SELECT id FROM t", 1)
			}
			holderTx := enginetest.TransactionBegin(t, holder)
			if phase == "cancel_source" {
				enginetest.SessionSQL(t, holder, "ALTER TABLE s ALTER COLUMN k SET NOT NULL")
			} else if phase == "delete" || phase == "repeatable_delete" {
				enginetest.SessionSQL(t, holder, "DELETE FROM t")
			} else {
				enginetest.SessionSQL(t, holder, "UPDATE t SET id=2")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := deleteUsingAsync(t, writer, ctx, "DELETE FROM t d USING s WHERE d.id=s.k")
			await := deleteUsingJoin(t, cancel, done)
			enginetest.LockPending(t, e, 1)
			if phase == "cancel_source" {
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				admitted := entry != nil && len(entry.Holders) >= 1
				if !admitted {
					t.Fatal("target was not admitted before source wait")
				}
			}
			if phase == "cancel_row" || phase == "cancel_source" {
				cancel()
				o := await()
				if !errors.Is(o.err, context.Canceled) || o.result.Count != 0 || o.result.Rows != nil {
					t.Fatal(o)
				}
				enginetest.TransactionEnd(t, holder, holderTx, false)
			} else {
				enginetest.TransactionEnd(t, holder, holderTx, phase != "rollback")
				if phase == "repeatable_update" || phase == "repeatable_delete" {
					o := await()
					var d *sqlstate.Error
					if o.result.Rows != nil || o.result.Count != 0 || !errors.As(o.err, &d) || d.Code != "40001" {
						t.Fatal(o)
					}
					enginetest.TransactionEnd(t, writer, writerTx, false)
				} else {
					count := int64(0)
					if phase == "rollback" {
						count = 1
					}
					o := await()
					if o.err != nil || o.result.Rows != nil || o.result.Count != count {
						t.Fatal(o, count)
					}
				}
			}
			if phase == "rollback" || phase == "delete" || phase == "repeatable_delete" {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t")
			} else if phase == "cancel_row" || phase == "cancel_source" {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t", 1)
			} else {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t", 2)
			}
			enginetest.TransactionRows(t, peer, "SELECT k FROM s", 1)
			enginetest.LockEmpty(t, e)
		})
	}
}
