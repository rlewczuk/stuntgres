package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func waitsDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}

func TestRelationLockWaitsNowait(t *testing.T) {
	for _, held := range []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.AccessExclusive, lock.ShareUpdateExclusive} {
		for _, requested := range []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.AccessExclusive, lock.ShareUpdateExclusive} {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, held); err != nil {
				t.Fatal(err)
			}
			request, err := e.Locks.StartAcquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, requested, lock.Policy{NoWait: true})
			conflict := held == lock.AccessExclusive || requested == lock.AccessExclusive || held == lock.ShareUpdateExclusive && requested == lock.ShareUpdateExclusive
			if request != nil || conflict && !errors.Is(err, lock.ErrUnavailable) || !conflict && err != nil {
				t.Fatalf("%d/%d: request=%v err=%v", held, requested, request, err)
			}
			lockState := e.Locks.Inspect(b.Transactions.Ticket)
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			bad := len(entry.Pending) != 0 || entry.Holders[a.Transactions.Ticket].Mode != held || conflict && len(lockState.Journals[b.Transactions.Ticket]) != 0
			if bad {
				t.Fatal("conditional failure changed holder/queue/journal")
			}
			enginetest.TransactionEnd(t, a, ra, false)
			enginetest.TransactionEnd(t, b, rb, false)
			enginetest.LockEmpty(t, e)
		}
	}
}

func TestRelationLockWaitsFairness(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		e := engine.New()
		a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra, rb, rc := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b), enginetest.TransactionBegin(t, c)
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		policy := lock.Policy{}
		if timeout {
			// Expiration is driven explicitly after both waiters are queued,
			// not by a race between the observer and an elapsed timer.
			policy.Timeout = time.Hour
		}
		ax, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if err != nil || ax == nil {
			t.Fatal("AX not queued", err)
		}
		follower, stopFollower := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopFollower()
		rx, err := e.Locks.StartAcquire(follower, c.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive, lock.Policy{})
		if err != nil || rx == nil {
			t.Fatal("RX barged", err)
		}
		enginetest.LockPending(t, e, 2)
		probe, err := e.Locks.StartAcquire(context.Background(), c.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive, lock.Policy{NoWait: true})
		if probe != nil || !errors.Is(err, lock.ErrUnavailable) {
			t.Fatal("NOWAIT barged", err)
		}
		if !timeout {
			cancel()
		}
		if timeout {
			waitsDiagnostic(t, e.Locks.ExpireWait(ax, ax.Deadline()), "55P03", "canceling statement due to lock timeout")
		}
		err = ax.Wait()
		cancel()
		if timeout {
			waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err = rx.Wait(); err != nil {
			t.Fatal(err)
		}
		lockState := e.Locks.Inspect(b.Transactions.Ticket)
		bad := lockState.Resources[lock.Resource{ObjectRef: ref}].Holders[a.Transactions.Ticket].Mode != lock.AccessShare || len(lockState.Journals[b.Transactions.Ticket]) != 0 || !ax.Terminal() || !rx.Terminal()
		if bad {
			t.Fatal("interruption damaged ownership/journal")
		}
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.TransactionEnd(t, c, rc, false)
		enginetest.LockEmpty(t, e)
	}
}

func TestRelationLockWaitsTimeoutPrecedence(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		lock, statement time.Time
		code            string
	}{
		{now.Add(-2 * time.Second), now.Add(-time.Second), "55P03"},
		{now.Add(-time.Second), now.Add(-2 * time.Second), "57014"},
		{now, now, "55P03"},
	} {
		err := lock.WaitError(context.Background(), lock.Policy{StatementDeadline: tc.statement}, tc.lock, now)
		message := "canceling statement due to lock timeout"
		if tc.code == "57014" {
			message = "canceling statement due to statement timeout"
		}
		waitsDiagnostic(t, err, tc.code, message)
	}
}

func waitsCommandAsync(e *core.Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, mustLockStatement(q), 0, nil)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			err = op.CommandError(err)
			op.Done()
		}
		done <- err
	}()
	return done
}

func TestRelationLockWaitsTimeout(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, tc := range []struct{ lock, statement, code, message string }{
			{"80ms", "0", "55P03", "canceling statement due to lock timeout"},
			{"80ms", "1s", "55P03", "canceling statement due to lock timeout"},
			{"1s", "80ms", "57014", "canceling statement due to statement timeout"},
			{"80ms", "80ms", "57014", "canceling statement due to statement timeout"},
		} {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "SET lock_timeout='"+tc.lock+"'")
			enginetest.SessionSQL(t, b, "SET statement_timeout='"+tc.statement+"'")
			ra := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			resource := lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), "t")}
			holder := a.Transactions.Ticket
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			done := waitsCommandAsync(e, b, ctx, "SELECT v FROM t", prepare)
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			poll := time.NewTicker(time.Millisecond)
			var outcome error
			for !joined {
				select {
				case outcome = <-done:
					joined = true
				case <-ctx.Done():
					select {
					case outcome = <-done:
						joined = true
					default:
						poll.Stop()
						t.Fatal("wait did not terminate", ctx.Err())
					}
				case <-poll.C:
					// A short SQL timeout may already have removed its
					// request before the parent gets scheduled. Observe
					// progress, but never require catching that transient.
					state := e.Locks.Inspect()
					if entry := state.Resources[resource]; entry.Holders[holder].Mode != lock.AccessExclusive || len(entry.Pending) > 1 {
						poll.Stop()
						t.Fatal("timeout wait damaged holder or queue")
					}
				}
			}
			poll.Stop()
			cancel()
			position := ""
			if tc.code == "55P03" {
				position = "15"
			}
			waitsDiagnostic(t, outcome, tc.code, tc.message, position)
			enginetest.LockPending(t, e, 0)
			if state := e.Locks.Inspect(); state.Resources[resource].Holders[holder].Mode != lock.AccessExclusive {
				t.Fatal("timeout released the blocking holder")
			}
			enginetest.TransactionEnd(t, a, ra, false)
			enginetest.SessionSQL(t, b, "SELECT v FROM t")
			enginetest.LockEmpty(t, e)
		}
	}
}

func TestRelationLockWaitsTimeoutCleanup(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	resource := lock.Resource{ObjectRef: ref}
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, resource, lock.AccessExclusive); err != nil {
		t.Fatal(err)
	}
	r, err := e.Locks.StartAcquire(context.Background(), b.Transactions.Ticket, resource, lock.AccessShare, lock.Policy{Timeout: 200 * time.Millisecond})
	if err != nil || r == nil {
		t.Fatal("timeout request did not queue", err)
	}
	// Use the actual request deadline. A real timer that already won must
	// still yield the exact timeout and retire the same registrations.
	e.Locks.ExpireWait(r, r.Deadline())
	waitsDiagnostic(t, r.Wait(), "55P03", "canceling statement due to lock timeout")
	if !r.Released() {
		t.Fatal("timed-out acquisition retained timer or context callback")
	}
	lockState := e.Locks.Inspect(b.Transactions.Ticket)
	entry := lockState.Resources[resource]
	if entry == nil || len(entry.Pending) != 0 || len(entry.Holders) != 1 || entry.Holders[a.Transactions.Ticket].Mode != lock.AccessExclusive {
		t.Fatal("timeout changed holder or retained waiter")
	}
	if _, retained := lockState.Journals[b.Transactions.Ticket][resource]; retained {
		t.Fatal("timeout retained resource in waiter journal")
	}
	enginetest.TransactionEnd(t, a, ra, false)
	if request, err := e.Locks.StartAcquire(context.Background(), b.Transactions.Ticket, resource, lock.AccessShare, lock.Policy{}); err != nil || request != nil {
		t.Fatalf("same-ticket recovery acquisition: request=%v err=%v", request, err)
	}
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.LockEmpty(t, e)
}
