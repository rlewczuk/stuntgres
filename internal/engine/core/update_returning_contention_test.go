package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestUpdateReturningCurrentPredecessor(t *testing.T) {
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "delete_rollback", "rollback", "repeatable_read"} {
		t.Run(lane, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v TEXT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(2,'old')")
			h := enginetest.TransactionBegin(t, holder)
			if lane == "repeatable_read" {
				enginetest.SessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			}
			hold := "UPDATE t SET v='fresh' WHERE id=2"
			if lane == "predicate_false" {
				hold = "UPDATE t SET id=9 WHERE id=2"
			}
			if lane == "peer_delete" || lane == "delete_rollback" {
				hold = "DELETE FROM t WHERE id=2"
			}
			enginetest.SessionSQL(t, holder, hold)
			query := "UPDATE t SET v='updated' WHERE id=2 RETURNING v,old.v,new.v"
			if lane == "no_where" {
				query = "UPDATE t SET v='updated' RETURNING v,old.v,new.v"
			}
			await := updateDeleteAsync(t, e, worker, context.Background(), query)
			enginetest.LockPending(t, e, 1)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			resource := lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[0].Row}
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[resource]
			actual := entry != nil && len(entry.Pending) == 1 && entry.Holders[holder.Transactions.Ticket].Mode&(lock.ForUpdate|lock.ForNoKeyUpdate) != 0
			if !actual {
				t.Fatal("not actual predecessor contention")
			}
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(2,'later')")
			enginetest.SessionSQL(t, peer, "COMMENT ON TABLE t IS 'compatible'")
			enginetest.TransactionEnd(t, holder, h, lane != "rollback" && lane != "delete_rollback")
			result, err := await()
			if lane == "repeatable_read" {
				enginetest.State(t, err, "40001")
				if result.Rows != nil || result.Count != 0 {
					t.Fatal(result)
				}
				enginetest.SessionSQL(t, worker, "ROLLBACK")
				enginetest.TransactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2, 2)
				enginetest.LockEmpty(t, e)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			want := [][]driver.Value{{"updated", "fresh", "updated"}}
			if lane == "rollback" || lane == "delete_rollback" {
				want = [][]driver.Value{{"updated", "old", "updated"}}
			}
			if lane == "predicate_false" || lane == "peer_delete" {
				want = [][]driver.Value{}
			}
			if got := enginetest.Tuples(result); !reflect.DeepEqual(got, want) || result.Count != int64(len(want)) {
				t.Fatal(got, want, result.Count)
			}
			if lane == "predicate_false" {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2, 9)
			} else if lane == "peer_delete" {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2)
			} else {
				enginetest.TransactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestUpdateReturningWaitCancellation(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1),(2)")
	h := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "DELETE FROM t WHERE id=2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	await := updateDeleteAsync(t, e, worker, ctx, "UPDATE t SET id=id+1 RETURNING id")
	enginetest.LockPending(t, e, 1)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	lockState := e.Locks.Inspect()
	first := lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[0].Row}
	second := lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[1].Row}
	entry := lockState.Resources[second]
	actual := entry != nil && len(entry.Pending) == 1 && lockState.Resources[first] != nil
	if actual {
		ticket := entry.Pending[0].Ticket
		actual = lockState.Resources[first].Holders[ticket].Mode&lock.ForNoKeyUpdate != 0
	}
	if !actual {
		t.Fatal("wait without independently observed earlier row grant")
	}
	cancel()
	result, err := await()
	if !errors.Is(err, context.Canceled) || result.Rows != nil || result.Count != 0 {
		t.Fatal(result, err)
	}
	enginetest.TransactionEnd(t, holder, h, false)
	enginetest.TransactionRows(t, peer, "SELECT id FROM t ORDER BY id", 1, 2)
	enginetest.LockEmpty(t, e)
}
