package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func deadlockCommand(t *testing.T, e *Engine, s *session.Session, q string, prepare bool) func() error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := waitsCommandAsync(e, s, ctx, q, prepare)
	joined := false
	await := func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("command failed to join")
			return nil
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			await()
		}
	})
	return await
}

func TestRelationDeadlockDiagnosticsPreparation(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, tc := range []struct{ create, query, token string }{
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t", "t"},
			{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,7)", "t"},
			{"CREATE TABLE t(id INT,v INT)", "UPDATE t SET v=7 WHERE id=1", "t"},
			{"CREATE TABLE t(id INT,v INT)", "DELETE FROM t WHERE id=1", "t"},
			{"CREATE TABLE t(id INT,v INT)", "  SELECT v FROM public.t", "public.t"},
			{"CREATE TABLE \"猫\"(id INT,v INT)", "/*猫*/ SELECT v FROM \"猫\"", "\"猫\""},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR KEY SHARE", "t"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR SHARE", "t"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR NO KEY UPDATE", "t"},
			{"CREATE TABLE t(id INT,v INT)", "SELECT v FROM t FOR UPDATE", "t"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR KEY SHARE", "public.t"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR SHARE", "public.t"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR NO KEY UPDATE", "public.t"},
			{"CREATE TABLE t(id INT,v INT)", "/*é*/ SELECT v FROM public.t FOR UPDATE", "public.t"},
		} {
			t.Run(fmt.Sprint(prepare)+"/"+tc.query, func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, a, tc.create)
				sessionSQL(t, a, "CREATE TABLE u(v INT)")
				originalName := b.Settings.Values["application_name"]
				ra := transactionBegin(t, a)
				rb := transactionBegin(t, b)
				t.Cleanup(func() { e.EndLockOwner(a.Transactions.Ticket); e.EndLockOwner(b.Transactions.Ticket) })
				name := "t"
				if strings.Contains(tc.create, "猫") {
					name = "\"猫\""
				}
				sessionSQL(t, a, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE")
				sessionSQL(t, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
				sessionSQL(t, b, "INSERT INTO u VALUES(202)")
				sessionSQL(t, b, "SET LOCAL application_name='victim'")
				first := deadlockCommand(t, e, a, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", false)
				lockPending(t, e, 1)
				second := deadlockCommand(t, e, b, tc.query, prepare)
				err := second()
				var d *sqlstate.Error
				position := strconv.Itoa(len([]rune(tc.query[:strings.LastIndex(tc.query, tc.token)])) + 1)
				if !errors.As(err, &d) || d.Code != "40P01" || d.Message != "deadlock detected" || d.Position != position || d.Hint != "See server log for query details." {
					t.Fatalf("analysis diagnostic %#v (%v), want position %s", d, err, position)
				}
				if err := first(); err != nil {
					t.Fatal("survivor must complete before victim explicit rollback", err)
				}
				if b.TransactionState() != session.TxAborted {
					t.Fatal("victim not aborted")
				}
				if got := b.Settings.Values["application_name"]; got != originalName || b.Settings.Actions != nil {
					t.Fatalf("victim settings not restored before release: %q, actions=%v", got, b.Settings.Actions)
				}
				op, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.Execute(op, mustLockStatement("SELECT v FROM u"), 0, nil)
				op.Done()
				waitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
				transactionEnd(t, b, rb, false)
				transactionEnd(t, a, ra, true)
				transactionRows(t, a, "SELECT v FROM u")
				fresh := transactionBegin(t, b)
				sessionSQL(t, b, "LOCK TABLE "+name+" IN ACCESS EXCLUSIVE MODE NOWAIT")
				sessionSQL(t, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT")
				transactionEnd(t, b, fresh, false)
				lockEmpty(t, e)
			})
		}
	}
}

func TestRelationDeadlockLifecycleCanceledPredecessor(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "CREATE TABLE u(v INT)")
	transactionBegin(t, a)
	transactionBegin(t, b)
	at, bt := a.Transactions.Ticket, b.Transactions.Ticket
	t.Cleanup(func() { e.EndLockOwner(at); e.EndLockOwner(bt) })
	x := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
	y := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "u")}
	deadlockRequest(t, e, at, x, lock.AccessExclusive)
	deadlockRequest(t, e, bt, y, lock.AccessExclusive)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := e.Locks.StartAcquire(ctx, at, y, lock.AccessExclusive, lock.Policy{})
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	cancel()
	// The canceled A->B request cannot make B->A a live cycle, even if its
	// asynchronous cancellation callback has not acquired manager metadata yet.
	follower := deadlockRequest(t, e, bt, x, lock.AccessExclusive)
	if err := deadlockResult(t, r); !errors.Is(err, context.Canceled) {
		t.Fatal("lost original cancellation", err)
	}
	terminal := follower.Terminal()
	if terminal {
		t.Fatal("canceled predecessor fabricated a deadlock")
	}
	e.EndLockOwner(at)
	if err := deadlockResult(t, follower); err != nil {
		t.Fatal(err)
	}
	e.EndLockOwner(bt)
	lockEmpty(t, e)
}
