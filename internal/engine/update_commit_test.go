package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestUpdateCoreMultiRelationCanceledFencedCommit(t *testing.T) {
	for _, fenced := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled_final_publication", true: "restore_fenced"}[fenced], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(v INT)")
			sessionSQL(t, peer, "CREATE TABLE u(v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1)")
			sessionSQL(t, peer, "INSERT INTO u VALUES(2)")
			tr := catalogRef(t, e, e.CatalogRead(), "t")
			ur := catalogRef(t, e, e.CatalogRead(), "u")
			roots := [2][]storage.RowVersion{e.storage.Relations[tr.Address].Versions, e.storage.Relations[ur.Address].Versions}
			saved := [2]storage.RowVersion{roots[0][0], roots[1][0]}
			frontier := e.visibility.commitSeq
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE t SET v=7")
			sessionSQL(t, a, "UPDATE u SET v=8")
			oldTicket := a.transactions.ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if fenced {
				if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);CREATE TABLE u(v INT);INSERT INTO t VALUES(91);INSERT INTO u VALUES(92);")); err != nil {
					t.Fatal(err)
				}
				_, err = e.Commit(op, tx)
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale replacement commit", err)
				}
			} else {
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				op.ctx = barrier
				done := make(chan error, 1)
				go func() { _, err := e.Commit(op, tx); done <- err }()
				select {
				case <-barrier.entered:
				case <-time.After(5 * time.Second):
					cancel()
					close(barrier.release)
					lockAwait(t, done, context.Canceled)
					t.Fatal("final publication barrier not reached")
				}
				// Both replacement roots have been constructed before final publication.
				cancel()
				close(barrier.release)
				lockAwait(t, done, context.Canceled)
				if e.visibility.commitSeq != frontier || &e.storage.Relations[tr.Address].Versions[0] != &roots[0][0] || &e.storage.Relations[ur.Address].Versions[0] != &roots[1][0] {
					t.Fatal("canceled batch installed a root/frontier")
				}
			}
			op.Done()
			if !reflect.DeepEqual(saved, [2]storage.RowVersion{roots[0][0], roots[1][0]}) {
				t.Fatal("retained predecessors mutated")
			}
			lockState := e.locks.Inspect(oldTicket)
			held := len(lockState.Journals[oldTicket])
			if held != 0 {
				t.Fatal("old row journal retained", held)
			}
			if a.TransactionState() != TxIdle {
				transactionEnd(t, a, tx, false)
			}
			lockEmpty(t, e)
			if fenced {
				transactionRows(t, peer, "SELECT v FROM t", 91)
				transactionRows(t, peer, "SELECT v FROM u", 92)
			} else {
				transactionRows(t, peer, "SELECT v FROM t", 1)
				transactionRows(t, peer, "SELECT v FROM u", 2)
			}
			sessionSQL(t, peer, "UPDATE t SET v=3")
			sessionSQL(t, peer, "UPDATE u SET v=4")
			lockEmpty(t, e)
		})
	}
}
