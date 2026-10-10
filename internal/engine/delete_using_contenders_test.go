package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func TestDeleteUsingQueuedContenderRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "described"}[prepared], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(lo INT,hi INT,n INT)", "INSERT INTO t VALUES(1,10),(9,5)", "INSERT INTO s VALUES(0,10,101),(0,30,202),(11,30,303)"} {
				sessionSQL(t, peer, q)
			}
			ast := statement(t, "DELETE FROM t x USING s WHERE x.id=1 AND x.v>=s.lo AND x.v<=s.hi RETURNING old.v,s.n,s.lo,s.hi")
			var expected *StatementDescription
			if prepared {
				op := transactionOperation(t, writer)
				description, err := e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &description
			}
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			original := e.storage[ref.Address].versions[0]
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "UPDATE t SET v=20 WHERE id=1")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var finishedScope *StatementScope
			done := make(chan deleteOutcome, 1)
			go func() {
				defer close(done)
				var result Result
				op, err := writer.Acquire(ctx)
				if err == nil {
					defer op.Done()
					finishedScope, err = e.StartStatement(op)
					if err == nil {
						defer func() {
							if !finishedScope.finished {
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
			lockPending(t, e, 1)
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: ref, row: original.row}]
			queued := entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == rowUpdate && entry.holders[holder.transactions.ticket] == noKeyUpdate
			e.locks.mu.Unlock()
			if !queued {
				t.Fatal("not an actual DELETE rowUpdate queue")
			}
			// Source edits and new pairs cannot replace the captured occurrences.
			sessionSQL(t, peer, "UPDATE s SET n=999")
			sessionSQL(t, peer, "INSERT INTO s VALUES(0,30,404)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
			sessionSQL(t, peer, "UPDATE t SET id=1 WHERE id=9")
			transactionEnd(t, holder, tx, true)
			got := await()
			var rows [][]driver.Value
			if got.err == nil && got.result.Rows != nil {
				rows = tuples(got.result)
				got.result.Rows.Close()
			}
			want := [][]driver.Value{{int64(20), int64(202), int64(0), int64(30)}}
			if got.err != nil || got.result.Count != 1 || !reflect.DeepEqual(rows, want) {
				t.Fatal("first recheck rejected/later pair must remain eligible", got, want)
			}
			if finishedScope == nil || len(finishedScope.querySources) != 0 {
				t.Fatal("admission leaked")
			}
			if testExport(original.values[1]) != int64(10) {
				t.Fatal("captured predecessor mutated")
			}
			transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 5, 30)
			lockEmpty(t, e)
		})
	}
}

func TestDeleteUsingContentionOutcomes(t *testing.T) {
	for _, phase := range []string{"rollback", "delete", "move_key", "repeatable_update", "repeatable_delete", "cancel_row", "cancel_source"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1)"} {
				sessionSQL(t, peer, q)
			}
			var writerTx TxRef
			if phase == "repeatable_update" || phase == "repeatable_delete" {
				sessionSQL(t, writer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				writerTx = writer.transactions.ref
				transactionRows(t, writer, "SELECT id FROM t", 1)
			}
			holderTx := transactionBegin(t, holder)
			if phase == "cancel_source" {
				sessionSQL(t, holder, "ALTER TABLE s ALTER COLUMN k SET NOT NULL")
			} else if phase == "delete" || phase == "repeatable_delete" {
				sessionSQL(t, holder, "DELETE FROM t")
			} else {
				sessionSQL(t, holder, "UPDATE t SET id=2")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := deleteUsingAsync(t, writer, ctx, "DELETE FROM t d USING s WHERE d.id=s.k")
			await := deleteUsingJoin(t, cancel, done)
			lockPending(t, e, 1)
			if phase == "cancel_source" {
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				e.locks.mu.Lock()
				entry := e.locks.entries[lockResource{ObjectRef: ref}]
				admitted := entry != nil && len(entry.holders) >= 1
				e.locks.mu.Unlock()
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
				transactionEnd(t, holder, holderTx, false)
			} else {
				transactionEnd(t, holder, holderTx, phase != "rollback")
				if phase == "repeatable_update" || phase == "repeatable_delete" {
					o := await()
					var d *sqlstate.Error
					if o.result.Rows != nil || o.result.Count != 0 || !errors.As(o.err, &d) || d.Code != "40001" {
						t.Fatal(o)
					}
					transactionEnd(t, writer, writerTx, false)
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
				transactionRows(t, peer, "SELECT id FROM t")
			} else if phase == "cancel_row" || phase == "cancel_source" {
				transactionRows(t, peer, "SELECT id FROM t", 1)
			} else {
				transactionRows(t, peer, "SELECT id FROM t", 2)
			}
			transactionRows(t, peer, "SELECT k FROM s", 1)
			lockEmpty(t, e)
		})
	}
}
