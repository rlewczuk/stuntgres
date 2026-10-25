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
	"stuntgres/internal/engine/session"
)

func TestUpdateFromQueuedTargetFrozenSources(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "predicate", "delete", "cancel", "reset", "rr_update", "rr_delete"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1,100),(2,200)", "INSERT INTO r VALUES(1),(2)"} {
				enginetest.SessionSQL(t, peer, q)
			}
			var writerTx ids.TxRef
			if mode == "rr_update" || mode == "rr_delete" {
				writerTx = enginetest.TransactionBegin(t, writer)
				enginetest.SessionSQL(t, writer, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
				enginetest.TransactionRows(t, writer, "SELECT v FROM t ORDER BY id", 10, 20)
			}
			tx := enginetest.TransactionBegin(t, holder)
			if mode == "delete" || mode == "rr_delete" {
				enginetest.SessionSQL(t, holder, "DELETE FROM t WHERE id=1")
			} else {
				enginetest.SessionSQL(t, holder, "UPDATE t SET v=11 WHERE id=1")
			}
			if mode == "predicate" {
				enginetest.SessionSQL(t, holder, "UPDATE t SET id=3 WHERE id=1")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type outcome struct {
				count int64
				rows  [][]driver.Value
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				op, err := writer.Acquire(ctx)
				var result engine.Result
				var rows [][]driver.Value
				if err == nil {
					result, err = e.Execute(op, mustLockStatement("UPDATE t SET v=t.v+s.n FROM s JOIN r USING(k) WHERE t.id=k RETURNING old.v,new.v,s.n"), 0, nil)
					if result.Rows != nil {
						rows = enginetest.Tuples(result)
						result.Rows.Close()
					}
					op.Done()
				}
				done <- outcome{result.Count, rows, err}
			}()
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
				t.Fatal("not a real target RowID queue barrier")
			}
			// The real target wait proves both source trees and candidates have
			// been captured. These later commits must never widen the write set.
			enginetest.SessionSQL(t, peer, "UPDATE s SET n=999")
			enginetest.SessionSQL(t, peer, "INSERT INTO s VALUES(1,400)")
			enginetest.SessionSQL(t, peer, "INSERT INTO r VALUES(1)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
			if mode == "cancel" {
				cancel()
			}
			if mode == "reset" {
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			enginetest.TransactionEnd(t, holder, tx, mode != "rollback" && mode != "cancel" && mode != "reset")
			var got outcome
			select {
			case got = <-done:
			case <-ctx.Done():
				if mode != "cancel" {
					t.Fatal(ctx.Err())
				}
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("cancel did not retire queued UPDATE")
				}
			}
			switch mode {
			case "cancel":
				if !errors.Is(got.err, context.Canceled) || got.count != 0 || got.rows != nil {
					t.Fatal(got)
				}
				enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 10, 20, 30)
			case "reset":
				if !errors.Is(got.err, ids.ErrCatalogReplaced) || got.count != 0 || got.rows != nil {
					t.Fatal(got)
				}
				enginetest.SessionSQL(t, peer, "CREATE TABLE t(v INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(7)")
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 7)
			case "rr_update", "rr_delete":
				message := "could not serialize access due to concurrent update"
				if mode == "rr_delete" {
					message = "could not serialize access due to concurrent delete"
				}
				waitsDiagnostic(t, got.err, "40001", message)
				if got.count != 0 || got.rows != nil || writer.TransactionState() != session.TxAborted {
					t.Fatal(got, writer.TransactionState())
				}
				enginetest.TransactionEnd(t, writer, writerTx, false)
				if mode == "rr_delete" {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 20, 30)
				} else {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 11, 20, 30)
				}
			default:
				if got.err != nil {
					t.Fatal(got.err)
				}
				want := [][]driver.Value{{int64(11), int64(111), int64(100)}, {int64(20), int64(220), int64(200)}}
				if mode == "rollback" {
					want[0] = []driver.Value{int64(10), int64(110), int64(100)}
				}
				if mode == "predicate" || mode == "delete" {
					want = want[1:]
				}
				if got.count != int64(len(want)) || !reflect.DeepEqual(got.rows, want) {
					t.Fatal(got, want)
				}
				if mode == "predicate" {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 11, 30, 220)
				} else if mode == "delete" {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 30, 220)
				} else if mode == "rollback" {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 30, 110, 220)
				} else {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 30, 111, 220)
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestUpdateFromQueuedSourceAdmissionAndCancellation(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1,100)", "INSERT INTO r VALUES(1)"} {
				enginetest.SessionSQL(t, holder, q)
			}
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "LOCK TABLE r IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			done := enginetest.LockAsync(e, writer, ctx, "UPDATE t SET v=s.n FROM s JOIN r USING(k) WHERE t.id=k RETURNING old.v,new.v,s.n", prepare)
			enginetest.LockPending(t, e, 1)
			tref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			sref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
			rref := enginetest.CatalogRef(t, e, e.CatalogRead(), "r")
			lockState := e.Locks.Inspect()
			te := lockState.Resources[lock.Resource{ObjectRef: tref}]
			se := lockState.Resources[lock.Resource{ObjectRef: sref}]
			re := lockState.Resources[lock.Resource{ObjectRef: rref}]
			admitted := te != nil && len(te.Holders) == 1 && se != nil && len(se.Holders) == 1 && re != nil && len(re.Pending) == 1
			if !admitted {
				t.Fatal("source queue did not retain target/first source grants")
			}
			if cancelled {
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
			}
			enginetest.SessionSQL(t, holder, "INSERT INTO s VALUES(1,200)")
			enginetest.SessionSQL(t, holder, "UPDATE t SET v=11")
			enginetest.TransactionEnd(t, holder, tx, true)
			if !cancelled {
				enginetest.LockAwait(t, done, nil)
			}
			cancel()
			if cancelled || prepare {
				enginetest.TransactionRows(t, holder, "SELECT v FROM t", 11)
			} else {
				op := enginetest.TransactionOperation(t, holder)
				result, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM t"), 0, nil)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				rows := enginetest.Tuples(result)
				result.Rows.Close()
				if len(rows) != 1 || (rows[0][0] != int64(100) && rows[0][0] != int64(200)) {
					t.Fatal(rows)
				}
			}
			enginetest.LockEmpty(t, e)
		}
	}
}
