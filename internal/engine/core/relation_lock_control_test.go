package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestRelationLockViewAfterWait(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
			ra := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			type answer struct {
				result core.Result
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				op, err := b.Acquire(context.Background())
				var r core.Result
				if err == nil {
					r, err = e.Execute(op, mustLockStatement("SELECT v FROM t ORDER BY v"), 0, nil)
					op.Done()
				}
				done <- answer{r, err}
			}()
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, ra, commit)
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				defer got.result.Rows.Close()
				want := [][]driver.Value{{int64(7)}}
				if commit {
					want = [][]driver.Value{{int64(1)}, {int64(7)}}
				}
				if rows := enginetest.Tuples(got.result); !reflect.DeepEqual(rows, want) {
					t.Fatal("waiting SELECT froze old/private view", rows, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SELECT remained blocked")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRelationLockReplacementWaitingPhases(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		for _, prepare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", q, prepare), func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				done := enginetest.LockAsync(e, b, context.Background(), q, prepare)
				enginetest.LockPending(t, e, 1)
				if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9)")); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.LockEmpty(t, e)
				enginetest.TransactionEnd(t, a, ra, false)
				enginetest.TransactionRows(t, b, "SELECT v FROM t", 9)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
func TestRelationLockReplacementGrantedReadmission(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	holder := enginetest.StartSession(t, e, nil)
	holderRef := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	op := enginetest.TransactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	stmt := enginetest.Statement(t, "SELECT v FROM t")
	if err = scope.Gate(stmt); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	// A real queued request relinquishes admission. Hold the writer reservation
	// while the grant is transferred, before the implicit reader can readmit.
	// No production test hooks: manager's actual holder journal proves grant.
	ticket := scope.LockTicket
	done := make(chan error, 1)
	go func() { _, err := scope.Execute(stmt, 0, nil); done <- err }()
	enginetest.LockPending(t, e, 1)
	e.ReplacementMu.Lock()
	enginetest.TransactionEnd(t, holder, holderRef, false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		held := false
		for _, entry := range lockState.Resources {
			if entry.Holders[ticket].Mode != 0 {
				held = true
			}
		}
		if held {
			break
		}
		if time.Now().After(deadline) {
			e.ReplacementMu.Unlock()
			t.Fatal("transferred grant absent")
		}
		runtime.Gosched()
	}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetEpoch(next.Epoch() + 1)
	e.Catalog.PublishLocked(next)
	epoch := next.Epoch()
	e.Mu.Unlock()
	e.ReplacementMu.Unlock()
	e.Locks.Fence(e.Catalog.Domain, epoch)
	enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
	if err = scope.Finish(ids.ErrCatalogReplaced); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	op.Done()
	enginetest.LockEmpty(t, e)
}
func TestRelationLockCancelEndRace(t *testing.T) {
	for range 32 {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := enginetest.TransactionBegin(t, a)
		rb := enginetest.TransactionBegin(t, b)
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- e.Locks.Acquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare)
		}()
		enginetest.LockPending(t, e, 1)
		ended := make(chan struct{})
		go func() { e.EndLockOwner(a.Transactions.Ticket); close(ended) }()
		cancel()
		err := <-done
		<-ended
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		lockState := e.Locks.Inspect()
		held := lock.Mode(0)
		if entry := lockState.Resources[lock.Resource{ObjectRef: ref}]; entry != nil {
			held = entry.Holders[b.Transactions.Ticket].Mode
		}
		if err != nil && held != 0 {
			t.Fatal("failed acquisition retained contribution")
		}
		if err == nil && held != lock.AccessShare {
			t.Fatal("grant lost journal")
		}
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.LockEmpty(t, e)
	}
}
