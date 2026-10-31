package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

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
