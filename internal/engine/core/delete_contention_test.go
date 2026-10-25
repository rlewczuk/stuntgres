package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

type deleteOutcome struct {
	result core.Result
	err    error
}

func deleteAsync(t *testing.T, s *session.Session, ctx context.Context, query string) <-chan deleteOutcome {
	t.Helper()
	ast := enginetest.Statement(t, query)
	done := make(chan deleteOutcome, 1)
	go func() {
		op, err := s.Acquire(ctx)
		var result core.Result
		if err == nil {
			result, err = core.EngineOf(s).Execute(op, ast, 0, nil)
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
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			c := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, c, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1)")
			ref := enginetest.TransactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v>=1")
			enginetest.LockPending(t, e, 1)
			target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			e.Mu.Lock()
			row := e.Storage.Relations[target.Address].Versions[0].Row
			e.Mu.Unlock()
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: target, Row: row}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Holders[a.Transactions.Ticket].Mode == lock.ForUpdate
			if !queued {
				t.Fatal("not waiting on original logical row")
			}
			enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 1)
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(2)")
			enginetest.TransactionEnd(t, a, ref, commit)
			want := int64(1)
			if commit {
				want = 0
			}
			deleteAwait(t, done, want, nil)
			enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 2)
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("different_rows", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
		enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
		ref := enginetest.TransactionBegin(t, a)
		deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		deleteAwait(t, deleteAsync(t, b, ctx, "DELETE FROM t WHERE v=2"), 1, nil)
		enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
		enginetest.TransactionEnd(t, a, ref, false)
		enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
		enginetest.LockEmpty(t, e)
	})
}

func TestDeleteCoreRowAuthority(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	resource := lock.Resource{ObjectRef: ref, Row: 1}
	manager := e.Locks
	if err := manager.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	if err := manager.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.ForUpdate); err != nil {
		t.Fatal("self reentry", err)
	}
	if err := manager.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref, Row: 2}, lock.ForUpdate); err != nil {
		t.Fatal("distinct row", err)
	}
	if err := manager.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal("ordinary SELECT", err)
	}
	for _, invalid := range []struct {
		resource lock.Resource
		mode     lock.Mode
	}{
		{lock.Resource{ObjectRef: ref}, lock.ForUpdate}, {resource, lock.AccessExclusive}, {resource, lock.AccessShare},
		{lock.Resource{ObjectRef: ref.Domain().Ref(ref.Epoch+1, ref.Address), Row: 1}, lock.ForUpdate},
	} {
		if err := manager.Acquire(context.Background(), b.Transactions.Ticket, invalid.resource, invalid.mode); err == nil {
			t.Fatal("invalid resource/mode accepted", invalid)
		}
	}
	copyTicket := *a.Transactions.Ticket
	if err := manager.Acquire(context.Background(), &copyTicket, resource, lock.ForUpdate); err == nil {
		t.Fatal("copied ticket accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, err := manager.StartAcquire(ctx, b.Transactions.Ticket, resource, lock.ForUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("row did not queue", request, err)
	}
	cancel()
	if err := request.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, a, ar, false)
	enginetest.TransactionEnd(t, b, br, false)
	enginetest.LockEmpty(t, e)
}

func TestDeleteCoreQueuedCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			c := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, c, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1),(2)")
			ar := enginetest.TransactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=2", 1)
			var br ids.TxRef
			if explicit {
				br = enginetest.TransactionBegin(t, b)
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := deleteAsync(t, b, ctx, "DELETE FROM t WHERE v<=2")
			enginetest.LockPending(t, e, 1)
			cancel()
			deleteAwait(t, done, 0, context.Canceled)
			if explicit {
				if b.TransactionState() != session.TxAborted || b.Transactions.Writes != nil || b.Transactions.Deletes != nil || b.Transactions.Ticket != nil {
					t.Fatal("explicit cancellation did not discard all private work")
				}
				enginetest.TransactionEnd(t, b, br, false)
			}
			enginetest.TransactionRows(t, c, "SELECT v FROM t ORDER BY v", 1, 2)
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
