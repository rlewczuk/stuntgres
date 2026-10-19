package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"

	"stuntgres/internal/engine/lock"
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
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra, rb := transactionBegin(t, a), transactionBegin(t, b)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, held); err != nil {
				t.Fatal(err)
			}
			request, err := e.locks.StartAcquire(context.Background(), b.transactions.ticket, lock.Resource{ObjectRef: ref}, requested, lock.Policy{NoWait: true})
			conflict := held == lock.AccessExclusive || requested == lock.AccessExclusive || held == lock.ShareUpdateExclusive && requested == lock.ShareUpdateExclusive
			if request != nil || conflict && !errors.Is(err, lock.ErrUnavailable) || !conflict && err != nil {
				t.Fatalf("%d/%d: request=%v err=%v", held, requested, request, err)
			}
			lockState := e.locks.Inspect(b.transactions.ticket)
			entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
			bad := len(entry.Pending) != 0 || entry.Holders[a.transactions.ticket].Mode != held || conflict && len(lockState.Journals[b.transactions.ticket]) != 0
			if bad {
				t.Fatal("conditional failure changed holder/queue/journal")
			}
			transactionEnd(t, a, ra, false)
			transactionEnd(t, b, rb, false)
			lockEmpty(t, e)
		}
	}
}

func TestRelationLockWaitsFairness(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		e := New()
		a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra, rb, rc := transactionBegin(t, a), transactionBegin(t, b), transactionBegin(t, c)
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
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
		ax, err := e.locks.StartAcquire(ctx, b.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy)
		if err != nil || ax == nil {
			t.Fatal("AX not queued", err)
		}
		follower, stopFollower := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopFollower()
		rx, err := e.locks.StartAcquire(follower, c.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive, lock.Policy{})
		if err != nil || rx == nil {
			t.Fatal("RX barged", err)
		}
		lockPending(t, e, 2)
		probe, err := e.locks.StartAcquire(context.Background(), c.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive, lock.Policy{NoWait: true})
		if probe != nil || !errors.Is(err, lock.ErrUnavailable) {
			t.Fatal("NOWAIT barged", err)
		}
		if !timeout {
			cancel()
		}
		if timeout {
			waitsDiagnostic(t, e.locks.ExpireWait(ax, ax.Deadline()), "55P03", "canceling statement due to lock timeout")
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
		lockState := e.locks.Inspect(b.transactions.ticket)
		bad := lockState.Resources[lock.Resource{ObjectRef: ref}].Holders[a.transactions.ticket].Mode != lock.AccessShare || len(lockState.Journals[b.transactions.ticket]) != 0 || !ax.Terminal() || !rx.Terminal()
		if bad {
			t.Fatal("interruption damaged ownership/journal")
		}
		transactionEnd(t, a, ra, false)
		transactionEnd(t, b, rb, false)
		transactionEnd(t, c, rc, false)
		lockEmpty(t, e)
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

func waitsCommandAsync(e *Engine, s *Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r Result
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
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "SET lock_timeout='"+tc.lock+"'")
			sessionSQL(t, b, "SET statement_timeout='"+tc.statement+"'")
			ra := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			resource := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "t")}
			holder := a.transactions.ticket
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
					state := e.locks.Inspect()
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
			lockPending(t, e, 0)
			if state := e.locks.Inspect(); state.Resources[resource].Holders[holder].Mode != lock.AccessExclusive {
				t.Fatal("timeout released the blocking holder")
			}
			transactionEnd(t, a, ra, false)
			sessionSQL(t, b, "SELECT v FROM t")
			lockEmpty(t, e)
		}
	}
}
