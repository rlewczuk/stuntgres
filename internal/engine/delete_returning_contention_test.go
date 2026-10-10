package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

func TestDeleteReturningCurrentPredecessor(t *testing.T) {
	for _, lane := range []string{"latest", "no_where", "predicate_false", "peer_delete", "rollback", "repeatable_read"} {
		t.Run(lane, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v TEXT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(2,'old')")
			h := transactionBegin(t, holder)
			if lane == "repeatable_read" {
				sessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			}
			hold := "UPDATE t SET v='fresh' WHERE id=2"
			if lane == "predicate_false" {
				hold = "UPDATE t SET id=9 WHERE id=2"
			}
			if lane == "peer_delete" {
				hold = "DELETE FROM t WHERE id=2"
			}
			sessionSQL(t, holder, hold)
			query := "DELETE FROM t WHERE id=2 RETURNING v,old.v,new.v"
			if lane == "no_where" {
				query = "DELETE FROM t RETURNING v,old.v,new.v"
			}
			await := updateDeleteAsync(t, e, worker, context.Background(), query)
			lockPending(t, e, 1)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			resource := lockResource{ObjectRef: ref, row: e.storage[ref.Address].versions[0].row}
			e.locks.mu.Lock()
			entry := e.locks.entries[resource]
			actual := entry != nil && len(entry.pending) == 1 && entry.holders[holder.transactions.ticket].mode&(rowUpdate|noKeyUpdate) != 0
			e.locks.mu.Unlock()
			if !actual {
				t.Fatal("not actual predecessor contention")
			}
			sessionSQL(t, peer, "INSERT INTO t VALUES(2,'later')")
			sessionSQL(t, peer, "COMMENT ON TABLE t IS 'compatible'")
			transactionEnd(t, holder, h, lane != "rollback")
			result, err := await()
			if lane == "repeatable_read" {
				state(t, err, "40001")
				if result.Rows != nil || result.Count != 0 {
					t.Fatal(result)
				}
				sessionSQL(t, worker, "ROLLBACK")
				transactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2, 2)
				lockEmpty(t, e)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			want := [][]driver.Value{{"fresh", "fresh", nil}}
			if lane == "rollback" {
				want = [][]driver.Value{{"old", "old", nil}}
			}
			if lane == "predicate_false" || lane == "peer_delete" {
				want = [][]driver.Value{}
			}
			if got := tuples(result); !reflect.DeepEqual(got, want) || result.Count != int64(len(want)) {
				t.Fatal(got, want, result.Count)
			}
			if lane == "predicate_false" {
				transactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2, 9)
			} else {
				transactionRows(t, peer, "SELECT id FROM t ORDER BY id", 2)
			}
			lockEmpty(t, e)
		})
	}
}

func TestDeleteReturningWaitCancellation(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2)")
	h := transactionBegin(t, holder)
	sessionSQL(t, holder, "DELETE FROM t WHERE id=2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	await := updateDeleteAsync(t, e, worker, ctx, "DELETE FROM t RETURNING id")
	lockPending(t, e, 1)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	e.locks.mu.Lock()
	first := lockResource{ObjectRef: ref, row: e.storage[ref.Address].versions[0].row}
	second := lockResource{ObjectRef: ref, row: e.storage[ref.Address].versions[1].row}
	entry := e.locks.entries[second]
	actual := entry != nil && len(entry.pending) == 1 && e.locks.entries[first] != nil
	if actual {
		ticket := entry.pending[0].ticket
		actual = e.locks.entries[first].holders[ticket].mode&rowUpdate != 0
	}
	e.locks.mu.Unlock()
	if !actual {
		t.Fatal("wait without independently observed earlier row grant")
	}
	cancel()
	result, err := await()
	if !errors.Is(err, context.Canceled) || result.Rows != nil || result.Count != 0 {
		t.Fatal(result, err)
	}
	transactionEnd(t, holder, h, false)
	transactionRows(t, peer, "SELECT id FROM t ORDER BY id", 1, 2)
	lockEmpty(t, e)
}
