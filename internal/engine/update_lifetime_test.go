package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func updateRowWorker(t *testing.T, e *Engine, s *Session, q string) func(error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := lockAsync(e, s, ctx, q, false)
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("row worker not joined")
			}
		}
	})
	return func(want error) {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			if (want == nil && err != nil) || (want != nil && !errors.Is(err, want)) {
				t.Fatal(err, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("row worker did not finish")
		}
	}
}

func TestUpdateCoreRowResourceLifetime(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=2")
	rows := sessionSQL(t, a, "SELECT v FROM t").Rows
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	join := updateRowWorker(t, e, b, "UPDATE t SET v=3")
	lockPending(t, e, 1)
	// Closing a result and ending each command did not release the owning row.
	a.RequestEnd(CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	join(nil)
	transactionRows(t, b, "SELECT v FROM t", 3)
	lockEmpty(t, e)
	// Capture an actual queued row request before a replacement fence. Old
	// request cancellation/owner callbacks cannot revoke a newer epoch's hold.
	holder := transactionBegin(t, b)
	sessionSQL(t, b, "UPDATE t SET v=4")
	c := sessionStart(t, e, nil)
	oldTx := transactionBegin(t, c)
	oldTicket := c.transactions.ticket
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	resource := lock.Resource{ObjectRef: ref, Row: e.storage[ref.Address].versions[0].row}
	request, err := e.locks.StartAcquire(oldCtx, oldTicket, resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9);")); err != nil {
		t.Fatal(err)
	}
	if err = request.Wait(); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	transactionEnd(t, b, holder, false)
	transactionEnd(t, c, oldTx, false)
	fresh := transactionBegin(t, c)
	sessionSQL(t, c, "UPDATE t SET v=10")
	newTicket := c.transactions.ticket
	newRef := catalogRef(t, e, e.CatalogRead(), "t")
	newResource := lock.Resource{ObjectRef: newRef, Row: e.storage[newRef.Address].versions[0].row}
	oldCancel()
	e.endLockOwner(oldTicket)
	e.locks.Fence(ref.Epoch)
	lockState := e.locks.Inspect(oldTicket)
	entry := lockState.Resources[newResource]
	held := lock.Mode(0)
	if entry != nil {
		held = entry.Holders[newTicket].Mode
	}
	oldJournal := len(lockState.Journals[oldTicket])
	if held != lock.ForNoKeyUpdate || oldJournal != 0 {
		t.Fatal("stale post-fence cleanup changed new journal", held, oldJournal)
	}
	join = updateRowWorker(t, e, b, "UPDATE t SET v=11")
	lockPending(t, e, 1)
	transactionEnd(t, c, fresh, false)
	join(nil)
	transactionRows(t, b, "SELECT v FROM t", 11)
	lockEmpty(t, e)
}

func TestUpdateCoreRestoreRowWaitLifecycle(t *testing.T) {
	for _, outcome := range []string{"failed", "canceled", "success"} {
		t.Run(outcome, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			saved := e.storage[ref.Address].versions
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE t SET v=2")
			oldTicket := a.transactions.ticket
			join := updateRowWorker(t, e, b, "UPDATE t SET v=3")
			lockPending(t, e, 1)
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer restoreCancel()
			input := "CREATE TABLE t(v INT);INSERT INTO t VALUES(9);"
			if outcome == "failed" {
				input += "UPDATE t SET v=7;"
			}
			reader := &commentGateReader{input: strings.NewReader(input), entered: make(chan struct{}), release: make(chan struct{})}
			restored := make(chan error, 1)
			go func() { restored <- e.Restore(restoreCtx, reader) }()
			released, restoreJoined := false, false
			defer func() {
				restoreCancel()
				if !released {
					close(reader.release)
				}
				if !restoreJoined {
					select {
					case <-restored:
					case <-time.After(5 * time.Second):
						t.Error("Restore worker not joined")
					}
				}
			}()
			select {
			case <-reader.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("Restore reader not reached")
			}
			// External Restore I/O holds neither live row resources nor replacement
			// writer admission: ordinary readers still see committed old heads.
			transactionRows(t, peer, "SELECT v FROM t", 1)
			if outcome == "canceled" {
				restoreCancel()
			}
			close(reader.release)
			released = true
			select {
			case err := <-restored:
				restoreJoined = true
				if outcome == "success" && err != nil {
					t.Fatal(err)
				}
				if outcome == "failed" && err == nil {
					t.Fatal("failed Restore succeeded")
				}
				if outcome == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Restore did not finish")
			}
			if testExport(saved[0].values[0]) != int64(1) {
				t.Fatal("Restore mutated retained old root")
			}
			if outcome == "success" {
				join(ErrCatalogReplaced)
				transactionEnd(t, a, tx, false)
				transactionRows(t, peer, "SELECT v FROM t", 9)
				if len(e.locks.Inspect(oldTicket).Journals[oldTicket]) != 0 {
					t.Fatal("fenced owner retained row resources")
				}
			} else {
				if catalogRef(t, e, e.CatalogRead(), "t") != ref || a.TransactionState() != TxActive {
					t.Fatal("failed/canceled Restore fenced eligible owner")
				}
				lockPending(t, e, 1)
				transactionRows(t, a, "SELECT v FROM t", 2)
				transactionEnd(t, a, tx, true)
				join(nil)
				transactionRows(t, peer, "SELECT v FROM t", 3)
			}
			sessionSQL(t, peer, "UPDATE t SET v=8")
			transactionRows(t, peer, "SELECT v FROM t", 8)
			lockEmpty(t, e)
		})
	}
}
