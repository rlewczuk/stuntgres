package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestUpdateCoreMultiRelationCanceledFencedCommit(t *testing.T) {
	for _, fenced := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled_final_publication", true: "restore_fenced"}[fenced], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, peer, "CREATE TABLE u(v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1)")
			enginetest.SessionSQL(t, peer, "INSERT INTO u VALUES(2)")
			tr := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			ur := enginetest.CatalogRef(t, e, e.CatalogRead(), "u")
			roots := [2][]storage.RowVersion{e.Storage.Relations[tr.Address].Versions, e.Storage.Relations[ur.Address].Versions}
			saved := [2]storage.RowVersion{roots[0][0], roots[1][0]}
			frontier := e.Visibility.CommitSeq()
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=7")
			enginetest.SessionSQL(t, a, "UPDATE u SET v=8")
			oldTicket := a.Transactions.Ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			if fenced {
				if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT);CREATE TABLE u(v INT);INSERT INTO t VALUES(91);INSERT INTO u VALUES(92);")); err != nil {
					t.Fatal(err)
				}
				_, err = e.Commit(op, tx)
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale replacement commit", err)
				}
			} else {
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				op.Ctx = barrier
				done := make(chan error, 1)
				go func() { _, err := e.Commit(op, tx); done <- err }()
				select {
				case <-barrier.entered:
				case <-time.After(5 * time.Second):
					cancel()
					close(barrier.release)
					enginetest.LockAwait(t, done, context.Canceled)
					t.Fatal("final publication barrier not reached")
				}
				// Both replacement roots have been constructed before final publication.
				cancel()
				close(barrier.release)
				enginetest.LockAwait(t, done, context.Canceled)
				if e.Visibility.CommitSeq() != frontier || &e.Storage.Relations[tr.Address].Versions[0] != &roots[0][0] || &e.Storage.Relations[ur.Address].Versions[0] != &roots[1][0] {
					t.Fatal("canceled batch installed a root/frontier")
				}
			}
			op.Done()
			if !reflect.DeepEqual(saved, [2]storage.RowVersion{roots[0][0], roots[1][0]}) {
				t.Fatal("retained predecessors mutated")
			}
			lockState := e.Locks.Inspect(oldTicket)
			held := len(lockState.Journals[oldTicket])
			if held != 0 {
				t.Fatal("old row journal retained", held)
			}
			if a.TransactionState() != session.TxIdle {
				enginetest.TransactionEnd(t, a, tx, false)
			}
			enginetest.LockEmpty(t, e)
			if fenced {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 91)
				enginetest.TransactionRows(t, peer, "SELECT v FROM u", 92)
			} else {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 1)
				enginetest.TransactionRows(t, peer, "SELECT v FROM u", 2)
			}
			enginetest.SessionSQL(t, peer, "UPDATE t SET v=3")
			enginetest.SessionSQL(t, peer, "UPDATE u SET v=4")
			enginetest.LockEmpty(t, e)
		})
	}
}

// Context.Err is the existing cooperative preflight seam, not a production hook.
// Arm only after staging. Entry validation checks twice; third check is the final
// publication boundary while both Session.mu and Engine.mu are held.
type transactionBarrierContext struct {
	context.Context
	checks  atomic.Int32
	entered chan struct{}
	release chan struct{}
	late    context.CancelFunc
}

func (c *transactionBarrierContext) Err() error {
	if c.checks.Add(1) == 3 {
		close(c.entered)
		<-c.release
		if c.late != nil {
			c.late()
			return nil
		}
	}
	return c.Context.Err()
}
