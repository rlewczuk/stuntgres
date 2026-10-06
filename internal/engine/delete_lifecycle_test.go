package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func TestDeleteCoreRowLifecycle(t *testing.T) {
	for _, action := range []string{"cancel", "end", "fence", "grant"} {
		t.Run(action, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ar, br := transactionBegin(t, a), transactionBegin(t, b)
			target := catalogRef(t, e, e.CatalogRead(), "t")
			resource := lockResource{ObjectRef: target, row: e.storage[target.Address].versions[0].row}
			manager := e.locks
			if err := manager.acquire(context.Background(), a.transactions.ticket, resource, rowUpdate); err != nil {
				t.Fatal(err)
			}
			canceled, stop := context.WithCancel(context.Background())
			stop()
			if request, err := manager.startAcquire(canceled, b.transactions.ticket, resource, rowUpdate, relationLockPolicy{}); request != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(request, err)
			}
			if request, err := manager.startAcquire(context.Background(), b.transactions.ticket, resource, rowUpdate, relationLockPolicy{nowait: true}); request != nil || !errors.Is(err, errRelationUnavailable) {
				t.Fatal(request, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request, err := manager.startAcquire(ctx, b.transactions.ticket, resource, rowUpdate, relationLockPolicy{timeout: time.Hour})
			if err != nil || request == nil {
				t.Fatal(request, err)
			}
			lockPending(t, e, 1)
			switch action {
			case "cancel":
				cancel()
			case "end":
				manager.end(b.transactions.ticket)
			case "fence":
				manager.fence(target.Epoch + 1)
			case "grant":
				manager.end(a.transactions.ticket)
				cancel()
			}
			want := map[string]error{"cancel": context.Canceled, "end": errRelationOwnerEnded, "fence": ErrCatalogReplaced, "grant": nil}[action]
			if err := request.wait(); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			manager.mu.Lock()
			terminal := request.terminal && !request.timer.Stop() && !request.stop()
			if action == "grant" {
				terminal = terminal && manager.entries[resource].holders[b.transactions.ticket] == rowUpdate
			}
			manager.mu.Unlock()
			if !terminal {
				t.Fatal("row terminal/transfer state invalid")
			}
			transactionEnd(t, a, ar, false)
			transactionEnd(t, b, br, false)
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreRowDeadlinePolicy(t *testing.T) {
	for _, kind := range []string{"lock", "statement"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ar, br := transactionBegin(t, a), transactionBegin(t, b)
			target := catalogRef(t, e, e.CatalogRead(), "t")
			resource := lockResource{ObjectRef: target, row: 1}
			manager := e.locks
			if err := manager.acquire(context.Background(), a.transactions.ticket, resource, rowUpdate); err != nil {
				t.Fatal(err)
			}
			request, err := manager.startAcquire(context.Background(), b.transactions.ticket, resource, rowUpdate, relationLockPolicy{timeout: time.Hour})
			if err != nil || request == nil {
				t.Fatal(request, err)
			}
			// Expire the real queued request under the existing manager authority, not a
			// production hook or timing sleep; settle owns its sole terminal transition.
			var notify *relationRequest
			manager.mu.Lock()
			if kind == "lock" {
				request.deadline = time.Now().Add(-time.Second)
			} else {
				request.policy.statementDeadline = time.Now().Add(-time.Second)
			}
			manager.settle(request.entry, &notify)
			manager.retire(resource, request.entry)
			manager.mu.Unlock()
			notifyRequests(notify)
			err = request.wait()
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
			if kind == "statement" {
				want.Code = "57014"
				want.Message = "canceling statement due to statement timeout"
			}
			var got *sqlstate.Error
			if !errors.As(err, &got) || *got != want {
				t.Fatal(err, got, want)
			}
			transactionEnd(t, a, ar, false)
			transactionEnd(t, b, br, false)
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreRelationLifecycle(t *testing.T) {
	for _, kind := range []string{"prepare", "direct", "cached"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ast := statement(t, "DELETE FROM t")
			var expected StatementDescription
			if kind == "cached" {
				op := transactionOperation(t, b)
				var err error
				expected, err = e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
			}
			ar := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan deleteOutcome, 1)
			go func() {
				op, err := b.Acquire(ctx)
				var result Result
				if err == nil {
					if kind == "prepare" {
						_, err = e.Describe(op, ast)
					} else {
						scope, startErr := e.StartStatement(op)
						err = startErr
						if err == nil {
							if kind == "cached" {
								scope.Expected = &expected
							}
							result, err = scope.Execute(ast, 0, nil)
							err = scope.Finish(err)
						}
					}
					op.Done()
				}
				done <- deleteOutcome{result, err}
			}()
			lockPending(t, e, 1)
			target := catalogRef(t, e, e.CatalogRead(), "t")
			e.locks.mu.Lock()
			entry := e.locks.entries[lockResource{ObjectRef: target}]
			queuedRX := entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == rowExclusive
			for resource := range e.locks.entries {
				if resource.row != 0 {
					queuedRX = false
				}
			}
			e.locks.mu.Unlock()
			if !queuedRX {
				t.Fatal("DELETE did not wait on RX relation before view capture")
			}
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			transactionEnd(t, a, ar, true)
			count := int64(1)
			if kind == "prepare" {
				count = 0
			}
			deleteAwait(t, done, count, nil)
			if kind == "prepare" {
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
				deleteCount(t, b, "DELETE FROM t", 1)
			} else {
				transactionRows(t, b, "SELECT v FROM t ORDER BY v")
			}
			lockEmpty(t, e)
		})
	}
	t.Run("explicit_prepare_retains_RX_only", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(v INT)")
		ref := transactionBegin(t, s)
		op := transactionOperation(t, s)
		description, err := e.Describe(op, statement(t, "DELETE FROM t"))
		op.Done()
		if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || len(s.transactions.deletes) != 0 {
			t.Fatal(description, err)
		}
		e.locks.mu.Lock()
		held := 0
		for resource, entry := range e.locks.entries {
			if resource.row != 0 || entry.holders[s.transactions.ticket] != rowExclusive {
				held = -100
				break
			}
			held++
		}
		e.locks.mu.Unlock()
		if held != 1 {
			t.Fatal("semantic Prepare holds rows or lost RX", held)
		}
		transactionEnd(t, s, ref, false)
		lockEmpty(t, e)
	})
}
