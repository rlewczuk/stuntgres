package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestDriverBatchUtilityQueuedReaderPublicationOrder(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			owner, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, owner, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(1)")
			table := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			catalog, store, frontier := e.Catalog.State, e.Storage.Relations[table.Address], e.Visibility.CommitSeq()
			m := batchUnitStart(t, owner, context.Background())
			t.Cleanup(func() { _ = m.end(context.Canceled) })
			if _, err := m.step(enginetest.Statement(t, "ALTER TABLE t ADD j INT DEFAULT 4")); err != nil {
				t.Fatal(err)
			}
			stmt := enginetest.Statement(t, "SELECT * FROM t")
			ctx, cancel := context.WithCancel(context.Background())
			joined := make(chan struct{})
			received := false
			type outcome struct {
				result engine.Result
				err    error
			}
			finished := make(chan outcome, 1)
			t.Cleanup(func() {
				cancel()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("queued reader failed to join")
					return
				}
				if !received {
					r := <-finished
					if r.result.Rows != nil {
						_ = r.result.Rows.Close()
					}
				}
			})
			go func() {
				defer close(joined)
				op, err := peer.Acquire(ctx)
				var result engine.Result
				if err == nil {
					result, err = e.Execute(op, stmt, 0, nil)
					op.Done()
				}
				finished <- outcome{result, err}
			}()
			enginetest.LockPending(t, e, 1)
			e.Mu.Lock()
			unchanged := e.Catalog.State == catalog && e.Storage.Relations[table.Address] == store && e.Visibility.CommitSeq() == frontier && len(e.Catalog.State.MustRelation(table.Address).Columns) == 1
			private := e.PrivateCatalogLocked(owner.Transactions.Catalog)
			e.Mu.Unlock()
			if !unchanged || len(private.MustRelation(table.Address).Columns) != 2 || owner.Transactions.Catalog.Storage[table.Address].Replacement == store {
				t.Fatal("queued observer did not see distinct real candidate and committed roots")
			}
			select {
			case r := <-finished:
				received = true
				if r.result.Rows != nil {
					_ = r.result.Rows.Close()
				}
				t.Fatal("reader completed while actual utility lock was held", r.err)
			default:
			}
			if commit {
				if err := m.end(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := m.step(enginetest.Statement(t, "SELECT missing"))
				if err == nil {
					t.Fatal("missing suffix failure")
				}
				if end := m.end(err); end != err {
					t.Fatal(end)
				}
			}
			var r outcome
			select {
			case r = <-finished:
				received = true
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not join after utility end")
			}
			<-joined
			if r.err != nil || r.result.Rows == nil {
				t.Fatal("released reader", r.err)
			}
			defer r.result.Rows.Close()
			want := [][]driver.Value{{int64(1)}}
			width := 1
			if commit {
				want = [][]driver.Value{{int64(1), int64(4)}}
				width = 2
			}
			if len(r.result.Rows.Schema()) != width || !reflect.DeepEqual(enginetest.Tuples(r.result), want) {
				t.Fatal("released reader observed wrong schema/data disposition", r.result.Rows.Schema())
			}
			if commit {
				if e.Catalog.State == catalog || e.Storage.Relations[table.Address] == store || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("utility roots not installed together")
				}
			} else {
				if e.Catalog.State != catalog || e.Storage.Relations[table.Address] != store || e.Visibility.CommitSeq() != frontier {
					t.Fatal("failed utility roots changed")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
