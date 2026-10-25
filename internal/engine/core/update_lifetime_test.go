package core_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func updateRowWorker(t *testing.T, e *core.Engine, s *session.Session, q string) func(error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := enginetest.LockAsync(e, s, ctx, q, false)
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
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
	rows := enginetest.SessionSQL(t, a, "SELECT v FROM t").Rows
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	join := updateRowWorker(t, e, b, "UPDATE t SET v=3")
	enginetest.LockPending(t, e, 1)
	// Closing a result and ending each command did not release the owning row.
	a.RequestEnd(session.CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	join(nil)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 3)
	enginetest.LockEmpty(t, e)
	// Capture an actual queued row request before a replacement fence. Old
	// request cancellation/owner callbacks cannot revoke a newer epoch's hold.
	holder := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, b, "UPDATE t SET v=4")
	c := enginetest.StartSession(t, e, nil)
	oldTx := enginetest.TransactionBegin(t, c)
	oldTicket := c.Transactions.Ticket
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	resource := lock.Resource{ObjectRef: ref, Row: e.Storage.Relations[ref.Address].Versions[0].Row}
	request, err := e.Locks.StartAcquire(oldCtx, oldTicket, resource, lock.ForNoKeyUpdate, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal(request, err)
	}
	if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9);")); err != nil {
		t.Fatal(err)
	}
	if err = request.Wait(); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, b, holder, false)
	enginetest.TransactionEnd(t, c, oldTx, false)
	fresh := enginetest.TransactionBegin(t, c)
	enginetest.SessionSQL(t, c, "UPDATE t SET v=10")
	newTicket := c.Transactions.Ticket
	newRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	newResource := lock.Resource{ObjectRef: newRef, Row: e.Storage.Relations[newRef.Address].Versions[0].Row}
	oldCancel()
	e.EndLockOwner(oldTicket)
	e.Locks.Fence(e.Catalog.Domain, ref.Epoch)
	lockState := e.Locks.Inspect(oldTicket)
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
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, c, fresh, false)
	join(nil)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 11)
	enginetest.LockEmpty(t, e)
}

func TestUpdateCoreRestoreRowWaitLifecycle(t *testing.T) {
	for _, outcome := range []string{"failed", "canceled", "success"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			saved := e.Storage.Relations[ref.Address].Versions
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
			oldTicket := a.Transactions.Ticket
			join := updateRowWorker(t, e, b, "UPDATE t SET v=3")
			enginetest.LockPending(t, e, 1)
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer restoreCancel()
			input := "CREATE TABLE t(v INT);INSERT INTO t VALUES(9);"
			if outcome == "failed" {
				input += "UPDATE t SET v=7;"
			}
			reader := &commentGateReader{input: strings.NewReader(input), entered: make(chan struct{}), release: make(chan struct{})}
			restored := make(chan error, 1)
			go func() { restored <- engine.Restore(restoreCtx, e, reader) }()
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
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 1)
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
			if enginetest.Export(saved[0].Values[0]) != int64(1) {
				t.Fatal("Restore mutated retained old root")
			}
			if outcome == "success" {
				join(ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 9)
				if len(e.Locks.Inspect(oldTicket).Journals[oldTicket]) != 0 {
					t.Fatal("fenced owner retained row resources")
				}
			} else {
				if enginetest.CatalogRef(t, e, e.CatalogRead(), "t") != ref || a.TransactionState() != session.TxActive {
					t.Fatal("failed/canceled Restore fenced eligible owner")
				}
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionRows(t, a, "SELECT v FROM t", 2)
				enginetest.TransactionEnd(t, a, tx, true)
				join(nil)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 3)
			}
			enginetest.SessionSQL(t, peer, "UPDATE t SET v=8")
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 8)
			enginetest.LockEmpty(t, e)
		})
	}
}

type commentGateReader struct {
	input            io.Reader
	entered, release chan struct{}
	first            bool
}

func (r *commentGateReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		close(r.entered)
		<-r.release
	}
	return r.input.Read(p)
}
