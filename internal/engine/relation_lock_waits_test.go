package engine

import (
	"context"
	"errors"
	"testing"
	"time"

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
	for _, held := range []relationLockMode{accessShare, rowExclusive, accessExclusive, shareUpdateExclusive} {
		for _, requested := range []relationLockMode{accessShare, rowExclusive, accessExclusive, shareUpdateExclusive} {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra, rb := transactionBegin(t, a), transactionBegin(t, b)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			if err := e.locks.acquire(context.Background(), a.transactions.ticket, ref, held); err != nil {
				t.Fatal(err)
			}
			request, err := e.locks.startAcquire(context.Background(), b.transactions.ticket, ref, requested, relationLockPolicy{nowait: true})
			conflict := held == accessExclusive || requested == accessExclusive || held == shareUpdateExclusive && requested == shareUpdateExclusive
			if request != nil || conflict && !errors.Is(err, errRelationUnavailable) || !conflict && err != nil {
				t.Fatalf("%d/%d: request=%v err=%v", held, requested, request, err)
			}
			e.locks.mu.Lock()
			entry := e.locks.entries[ref]
			bad := len(entry.pending) != 0 || entry.holders[a.transactions.ticket] != held || conflict && len(b.transactions.ticket.resources) != 0
			e.locks.mu.Unlock()
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
		if err := e.locks.acquire(context.Background(), a.transactions.ticket, ref, accessShare); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		policy := relationLockPolicy{}
		if timeout {
			policy.timeout = 200 * time.Millisecond
		}
		ax, err := e.locks.startAcquire(ctx, b.transactions.ticket, ref, accessExclusive, policy)
		if err != nil || ax == nil {
			t.Fatal("AX not queued", err)
		}
		follower, stopFollower := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopFollower()
		rx, err := e.locks.startAcquire(follower, c.transactions.ticket, ref, rowExclusive, relationLockPolicy{})
		if err != nil || rx == nil {
			t.Fatal("RX barged", err)
		}
		lockPending(t, e, 2)
		probe, err := e.locks.startAcquire(context.Background(), c.transactions.ticket, ref, rowExclusive, relationLockPolicy{nowait: true})
		if probe != nil || !errors.Is(err, errRelationUnavailable) {
			t.Fatal("NOWAIT barged", err)
		}
		if !timeout {
			cancel()
		}
		err = ax.wait()
		cancel()
		if timeout {
			waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout")
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err = rx.wait(); err != nil {
			t.Fatal(err)
		}
		e.locks.mu.Lock()
		bad := e.locks.entries[ref].holders[a.transactions.ticket] != accessShare || len(b.transactions.ticket.resources) != 0 || !ax.terminal || !rx.terminal
		e.locks.mu.Unlock()
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
		err := relationWaitError(context.Background(), relationLockPolicy{statementDeadline: tc.statement}, tc.lock, now)
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
			done := waitsCommandAsync(e, b, context.Background(), "SELECT v FROM t", prepare)
			lockPending(t, e, 1)
			select {
			case err := <-done:
				position := ""
				if tc.code == "55P03" {
					position = "15"
				}
				waitsDiagnostic(t, err, tc.code, tc.message, position)
			case <-time.After(5 * time.Second):
				t.Fatal("wait did not terminate")
			}
			lockPending(t, e, 0)
			transactionEnd(t, a, ra, false)
			sessionSQL(t, b, "SELECT v FROM t")
			lockEmpty(t, e)
		}
	}
}

func TestRelationLockWaitsTimeoutPerAcquisition(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "CREATE TABLE u(v INT)")
	rb := transactionBegin(t, b)
	policy := relationLockPolicy{timeout: 200 * time.Millisecond}
	for _, name := range []string{"t", "u"} {
		ref := catalogRef(t, e, e.CatalogRead(), name)
		ra := transactionBegin(t, a)
		if err := e.locks.acquire(context.Background(), a.transactions.ticket, ref, accessExclusive); err != nil {
			t.Fatal(err)
		}
		r, err := e.locks.startAcquire(context.Background(), b.transactions.ticket, ref, accessShare, policy)
		if err != nil || r == nil {
			t.Fatal(err)
		}
		time.Sleep(120 * time.Millisecond)
		transactionEnd(t, a, ra, false)
		if err = r.wait(); err != nil {
			t.Fatal(err)
		}
	}
	transactionEnd(t, b, rb, false)
	lockEmpty(t, e)
}
