package engine

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

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestRelationLockViewAfterWait(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7)")
			ra := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			type answer struct {
				result Result
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				op, err := b.Acquire(context.Background())
				var r Result
				if err == nil {
					r, err = e.Execute(op, mustLockStatement("SELECT v FROM t ORDER BY v"), 0, nil)
					op.Done()
				}
				done <- answer{r, err}
			}()
			lockPending(t, e, 1)
			transactionEnd(t, a, ra, commit)
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
				if rows := tuples(got.result); !reflect.DeepEqual(rows, want) {
					t.Fatal("waiting SELECT froze old/private view", rows, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SELECT remained blocked")
			}
			lockEmpty(t, e)
		})
	}
}
func TestRelationLockReplacementWaitingPhases(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		for _, prepare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", q, prepare), func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				done := lockAsync(e, b, context.Background(), q, prepare)
				lockPending(t, e, 1)
				if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9)")); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ids.ErrCatalogReplaced)
				lockEmpty(t, e)
				transactionEnd(t, a, ra, false)
				transactionRows(t, b, "SELECT v FROM t", 9)
				lockEmpty(t, e)
			})
		}
	}
}
func TestRelationLockReplacementGrantedReadmission(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	holder := sessionStart(t, e, nil)
	holderRef := transactionBegin(t, holder)
	sessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	op := transactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	stmt := statement(t, "SELECT v FROM t")
	if err = scope.gate(stmt); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	// A real queued request relinquishes admission. Hold the writer reservation
	// while the grant is transferred, before the implicit reader can readmit.
	// No production test hooks: manager's actual holder journal proves grant.
	ticket := scope.ticket
	done := make(chan error, 1)
	go func() { _, err := scope.Execute(stmt, 0, nil); done <- err }()
	lockPending(t, e, 1)
	e.replacementMu.Lock()
	transactionEnd(t, holder, holderRef, false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
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
			e.replacementMu.Unlock()
			t.Fatal("transferred grant absent")
		}
		runtime.Gosched()
	}
	e.mu.Lock()
	next := catalog.CloneState(e.catalog.state)
	next.SetEpoch(next.Epoch() + 1)
	e.catalog.state = next
	epoch := next.Epoch()
	e.mu.Unlock()
	e.replacementMu.Unlock()
	e.locks.Fence(epoch)
	lockAwait(t, done, ids.ErrCatalogReplaced)
	if err = scope.Finish(ids.ErrCatalogReplaced); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	op.Done()
	lockEmpty(t, e)
}
func TestRelationLockCancelEndRace(t *testing.T) {
	for range 32 {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		rb := transactionBegin(t, b)
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		if err := e.locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- e.locks.Acquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare)
		}()
		lockPending(t, e, 1)
		ended := make(chan struct{})
		go func() { e.endLockOwner(a.Transactions.Ticket); close(ended) }()
		cancel()
		err := <-done
		<-ended
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		lockState := e.locks.Inspect()
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
		transactionEnd(t, a, ra, false)
		transactionEnd(t, b, rb, false)
		lockEmpty(t, e)
	}
}
