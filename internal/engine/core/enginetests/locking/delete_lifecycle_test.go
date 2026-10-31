package locking_test

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

func TestDeleteCoreRowLifecycle(t *testing.T) {
	for _, action := range []string{"cancel", "end", "fence", "grant"} {
		t.Run(action, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
			target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			resource := lock.Resource{ObjectRef: target, Row: e.Storage.Relations[target.Address].Versions[0].Row}
			manager := e.Locks
			if err := manager.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.ForUpdate); err != nil {
				t.Fatal(err)
			}
			canceled, stop := context.WithCancel(context.Background())
			stop()
			if request, err := manager.StartAcquire(canceled, b.Transactions.Ticket, resource, lock.ForUpdate, lock.Policy{}); request != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(request, err)
			}
			if request, err := manager.StartAcquire(context.Background(), b.Transactions.Ticket, resource, lock.ForUpdate, lock.Policy{NoWait: true}); request != nil || !errors.Is(err, lock.ErrUnavailable) {
				t.Fatal(request, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request, err := manager.StartAcquire(ctx, b.Transactions.Ticket, resource, lock.ForUpdate, lock.Policy{Timeout: time.Hour})
			if err != nil || request == nil {
				t.Fatal(request, err)
			}
			enginetest.LockPending(t, e, 1)
			switch action {
			case "cancel":
				cancel()
			case "end":
				manager.End(b.Transactions.Ticket)
			case "fence":
				manager.Fence(target.Domain(), target.Epoch+1)
			case "grant":
				manager.End(a.Transactions.Ticket)
				cancel()
			}
			want := map[string]error{"cancel": context.Canceled, "end": lock.ErrOwnerEnded, "fence": ids.ErrCatalogReplaced, "grant": nil}[action]
			if err := request.Wait(); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			terminal := request.Released()
			if action == "grant" {
				terminal = terminal && manager.Inspect().Resources[resource].Holders[b.Transactions.Ticket].Mode == lock.ForUpdate
			}
			if !terminal {
				t.Fatal("row terminal/transfer state invalid")
			}
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.TransactionEnd(t, b, br, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteCoreRowDeadlinePolicy(t *testing.T) {
	for _, kind := range []string{"lock", "statement"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
			target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			resource := lock.Resource{ObjectRef: target, Row: 1}
			manager := e.Locks
			if err := manager.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.ForUpdate); err != nil {
				t.Fatal(err)
			}
			policy := lock.Policy{Timeout: time.Hour}
			if kind == "statement" {
				policy.StatementDeadline = time.Now().Add(time.Minute)
			}
			request, err := manager.StartAcquire(context.Background(), b.Transactions.Ticket, resource, lock.ForUpdate, policy)
			if err != nil || request == nil {
				t.Fatal(request, err)
			}
			// Expire the real queued request at its own deadline through the manager's
			// terminal path, not a timer race or sleep.
			due := request.Deadline()
			if kind == "statement" {
				due = policy.StatementDeadline
			}
			manager.ExpireWait(request, due)
			err = request.Wait()
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
			if kind == "statement" {
				want.Code = "57014"
				want.Message = "canceling statement due to statement timeout"
			}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(err, got, want)
			}
			enginetest.TransactionEnd(t, a, ar, false)
			enginetest.TransactionEnd(t, b, br, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
