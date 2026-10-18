package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestRelationLockWaitsLifecycle(t *testing.T) {
	for _, action := range []string{"cancel", "end", "fence", "grant"} {
		t.Run(action, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra, rb := transactionBegin(t, a), transactionBegin(t, b)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			if err := e.locks.Acquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := e.locks.StartAcquire(ctx, b.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, lock.Policy{Timeout: time.Hour})
			if err != nil || r == nil {
				t.Fatal(err)
			}
			lockPending(t, e, 1)
			switch action {
			case "cancel":
				cancel()
			case "end":
				e.endLockOwner(b.transactions.ticket)
			case "fence":
				e.locks.Fence(ref.Epoch + 1)
			case "grant":
				e.endLockOwner(a.transactions.ticket)
				cancel()
			}
			err = r.Wait()
			want := map[string]error{"cancel": context.Canceled, "end": lock.ErrOwnerEnded, "fence": ErrCatalogReplaced, "grant": nil}[action]
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if !r.Released() {
				t.Fatal("terminal registrations remained live")
			}
			transactionEnd(t, a, ra, false)
			transactionEnd(t, b, rb, false)
			lockEmpty(t, e)
		})
	}
}

func TestRelationLockWaitsSettings(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"lock_timeout": "1500ms"}, calls: map[string]int{}}
	s := sessionStart(t, e, provider)
	check := func(want string) {
		t.Helper()
		got := tuples(sessionSQL(t, s, "SHOW lock_timeout"))
		if len(got) != 1 || got[0][0] != want {
			t.Fatalf("SHOW=%v want %s", got, want)
		}
	}
	check("1500ms")
	provider.values["lock_timeout"] = "2s"
	for _, q := range []string{"SET lock_timeout=DEFAULT", "RESET lock_timeout", "RESET ALL"} {
		sessionSQL(t, s, "SET lock_timeout='3s'")
		sessionSQL(t, s, q)
		check("1500ms")
	}
	if provider.calls["lock_timeout"] != 1 {
		t.Fatal("default not captured once", provider.calls)
	}
	for _, commit := range []bool{false, true} {
		ref := transactionBegin(t, s)
		sessionSQL(t, s, "SET LOCAL lock_timeout='20ms'")
		check("20ms")
		transactionEnd(t, s, ref, commit)
		check("1500ms")
		ref = transactionBegin(t, s)
		sessionSQL(t, s, "SET lock_timeout='1s'")
		transactionEnd(t, s, ref, commit)
		if commit {
			check("1s")
		} else {
			check("1500ms")
		}
		sessionSQL(t, s, "RESET lock_timeout")
	}
	for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"1000us", "1ms"}, {"0.5ms", "0"}} {
		sessionSQL(t, s, "SET lock_timeout='"+tc.input+"'")
		check(tc.want)
	}
	other := sessionStart(t, e, provider)
	if got := tuples(sessionSQL(t, other, "SHOW lock_timeout")); len(got) != 1 || got[0][0] != "2s" {
		t.Fatal("new physical session did not capture new provider default", got)
	}
}

func TestRelationLockWaitsAbort(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		e := New()
		a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		sessionSQL(t, a, "CREATE TABLE u(v INT)")
		sessionSQL(t, b, "SET lock_timeout='80ms'")
		ra, rb := transactionBegin(t, a), transactionBegin(t, b)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		sessionSQL(t, b, "INSERT INTO u VALUES(9)")
		done := waitsCommandAsync(e, b, context.Background(), "SELECT v FROM t", prepare)
		lockPending(t, e, 1)
		select {
		case err := <-done:
			waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "15")
		case <-time.After(5 * time.Second):
			t.Fatal("timeout did not finish")
		}
		op, _, err := b.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, mustLockStatement("SELECT v FROM u"), 0, nil)
		err = op.CommandError(err)
		op.Done()
		waitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		rc := transactionBegin(t, c)
		sessionSQL(t, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
		if got := tuples(sessionSQL(t, c, "SELECT v FROM u")); len(got) != 0 {
			t.Fatal("failed private writes published", got)
		}
		transactionEnd(t, c, rc, false)
		transactionEnd(t, b, rb, false)
		transactionEnd(t, a, ra, false)
		lockEmpty(t, e)
	}
}

func TestRelationLockWaitsNowaitAbort(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "CREATE TABLE u(v INT)")
	ra, rb := transactionBegin(t, a), transactionBegin(t, b)
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	sessionSQL(t, b, "INSERT INTO u VALUES(9)")
	op, _, err := b.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, mustLockStatement("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"), 0, nil)
	err = op.CommandError(err)
	op.Done()
	waitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
	if b.TransactionState() != TxAborted {
		t.Fatal("NOWAIT did not abort explicit transaction")
	}
	rc := transactionBegin(t, c)
	sessionSQL(t, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT")
	if got := tuples(sessionSQL(t, c, "SELECT v FROM u")); len(got) != 0 {
		t.Fatal("NOWAIT published prior writes", got)
	}
	transactionEnd(t, c, rc, false)
	transactionEnd(t, b, rb, false)
	transactionEnd(t, a, ra, false)
	lockEmpty(t, e)
}
