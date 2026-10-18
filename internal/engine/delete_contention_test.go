package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

type deleteOutcome struct {
	result Result
	err    error
}

func deleteAsync(t *testing.T, s *Session, ctx context.Context, query string) <-chan deleteOutcome {
	t.Helper()
	ast := statement(t, query)
	done := make(chan deleteOutcome, 1)
	go func() {
		op, err := s.Acquire(ctx)
		var result Result
		if err == nil {
			result, err = s.Engine().Execute(op, ast, 0, nil)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	return done
}
func deleteAwait(t *testing.T, done <-chan deleteOutcome, count int64, want error) {
	t.Helper()
	select {
	case got := <-done:
		if !errors.Is(got.err, want) || got.result.Rows != nil || got.result.Count != count {
			t.Fatal(got, count, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE did not finish")
	}
}

func TestDeleteCoreContention(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "peer_rollback", true: "peer_commit"}[commit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			c := sessionStart(t, e, nil)
			sessionSQL(t, c, "CREATE TABLE t(v INT)")
			sessionSQL(t, c, "INSERT INTO t VALUES(1)")
			ref := transactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v>=1")
			lockPending(t, e, 1)
			target := catalogRef(t, e, e.CatalogRead(), "t")
			e.mu.Lock()
			row := e.storage[target.Address].versions[0].row
			e.mu.Unlock()
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: target, Row: row}]
			queued := entry != nil && len(entry.pending) == 1 && entry.holders[a.transactions.ticket].Mode == rowUpdate
			e.locks.mu.Unlock()
			if !queued {
				t.Fatal("not waiting on original logical row")
			}
			transactionRows(t, c, "SELECT v FROM t ORDER BY v", 1)
			sessionSQL(t, c, "INSERT INTO t VALUES(2)")
			transactionEnd(t, a, ref, commit)
			want := int64(1)
			if commit {
				want = 0
			}
			deleteAwait(t, done, want, nil)
			transactionRows(t, c, "SELECT v FROM t ORDER BY v", 2)
			lockEmpty(t, e)
		})
	}
	t.Run("different_rows", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, b, "CREATE TABLE t(v INT)")
		sessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
		ref := transactionBegin(t, a)
		deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		deleteAwait(t, deleteAsync(t, b, ctx, "DELETE FROM t WHERE v=2"), 1, nil)
		transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
		transactionEnd(t, a, ref, false)
		transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
		lockEmpty(t, e)
	})
}

func TestDeleteCoreRowAuthority(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	resource := lockResource{ObjectRef: ref, Row: 1}
	manager := e.locks
	if err := manager.Acquire(context.Background(), a.transactions.ticket, resource, rowUpdate); err != nil {
		t.Fatal(err)
	}
	if err := manager.Acquire(context.Background(), a.transactions.ticket, resource, rowUpdate); err != nil {
		t.Fatal("self reentry", err)
	}
	if err := manager.Acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: ref, Row: 2}, rowUpdate); err != nil {
		t.Fatal("distinct row", err)
	}
	if err := manager.Acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: ref}, accessShare); err != nil {
		t.Fatal("ordinary SELECT", err)
	}
	for _, invalid := range []struct {
		resource lockResource
		mode     relationLockMode
	}{
		{lockResource{ObjectRef: ref}, rowUpdate}, {resource, accessExclusive}, {resource, accessShare},
		{lockResource{ObjectRef: ref.Domain().Ref(ref.Epoch+1, ref.Address), Row: 1}, rowUpdate},
	} {
		if err := manager.Acquire(context.Background(), b.transactions.ticket, invalid.resource, invalid.mode); err == nil {
			t.Fatal("invalid resource/mode accepted", invalid)
		}
	}
	copyTicket := *a.transactions.ticket
	if err := manager.Acquire(context.Background(), &copyTicket, resource, rowUpdate); err == nil {
		t.Fatal("copied ticket accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := manager.StartAcquire(ctx, b.transactions.ticket, resource, rowUpdate, relationLockPolicy{})
	if err != nil || request == nil {
		t.Fatal("row did not queue", request, err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	transactionEnd(t, a, ar, false)
	transactionEnd(t, b, br, false)
	lockEmpty(t, e)
}

func TestDeleteCoreQueuedCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			c := sessionStart(t, e, nil)
			sessionSQL(t, c, "CREATE TABLE t(v INT)")
			sessionSQL(t, c, "INSERT INTO t VALUES(1),(2)")
			ar := transactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=2", 1)
			var br ids.TxRef
			if explicit {
				br = transactionBegin(t, b)
				sessionSQL(t, b, "INSERT INTO t VALUES(7)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v<=2")
			lockPending(t, e, 1)
			cancel()
			deleteAwait(t, done, 0, context.Canceled)
			if explicit {
				if b.TransactionState() != TxAborted || b.transactions.writes != nil || b.transactions.deletes != nil || b.transactions.ticket != nil {
					t.Fatal("explicit cancellation did not discard all private work")
				}
				transactionEnd(t, b, br, false)
			}
			transactionRows(t, c, "SELECT v FROM t ORDER BY v", 1, 2)
			transactionEnd(t, a, ar, false)
			lockEmpty(t, e)
		})
	}
}
