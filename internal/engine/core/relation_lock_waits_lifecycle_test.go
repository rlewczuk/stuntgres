package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestRelationLockWaitsLifecycle(t *testing.T) {
	for _, action := range []string{"cancel", "end", "fence", "grant"} {
		t.Run(action, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, err := e.Locks.StartAcquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, lock.Policy{Timeout: time.Hour})
			if err != nil || r == nil {
				t.Fatal(err)
			}
			enginetest.LockPending(t, e, 1)
			switch action {
			case "cancel":
				cancel()
			case "end":
				e.EndLockOwner(b.Transactions.Ticket)
			case "fence":
				e.Locks.Fence(e.Catalog.Domain, ref.Epoch+1)
			case "grant":
				e.EndLockOwner(a.Transactions.Ticket)
				cancel()
			}
			err = r.Wait()
			want := map[string]error{"cancel": context.Canceled, "end": lock.ErrOwnerEnded, "fence": ids.ErrCatalogReplaced, "grant": nil}[action]
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if !r.Released() {
				t.Fatal("terminal registrations remained live")
			}
			enginetest.TransactionEnd(t, a, ra, false)
			enginetest.TransactionEnd(t, b, rb, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestRelationLockWaitsSettings(t *testing.T) {
	e := engine.New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"lock_timeout": "1500ms"}, calls: map[string]int{}}
	s := enginetest.StartSession(t, e, provider)
	check := func(want string) {
		t.Helper()
		got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SHOW lock_timeout"))
		if len(got) != 1 || got[0][0] != want {
			t.Fatalf("SHOW=%v want %s", got, want)
		}
	}
	check("1500ms")
	provider.values["lock_timeout"] = "2s"
	for _, q := range []string{"SET lock_timeout=DEFAULT", "RESET lock_timeout", "RESET ALL"} {
		enginetest.SessionSQL(t, s, "SET lock_timeout='3s'")
		enginetest.SessionSQL(t, s, q)
		check("1500ms")
	}
	if provider.calls["lock_timeout"] != 1 {
		t.Fatal("default not captured once", provider.calls)
	}
	for _, commit := range []bool{false, true} {
		ref := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "SET LOCAL lock_timeout='20ms'")
		check("20ms")
		enginetest.TransactionEnd(t, s, ref, commit)
		check("1500ms")
		ref = enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "SET lock_timeout='1s'")
		enginetest.TransactionEnd(t, s, ref, commit)
		if commit {
			check("1s")
		} else {
			check("1500ms")
		}
		enginetest.SessionSQL(t, s, "RESET lock_timeout")
	}
	for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"1000us", "1ms"}, {"0.5ms", "0"}} {
		enginetest.SessionSQL(t, s, "SET lock_timeout='"+tc.input+"'")
		check(tc.want)
	}
	other := enginetest.StartSession(t, e, provider)
	if got := enginetest.Tuples(enginetest.SessionSQL(t, other, "SHOW lock_timeout")); len(got) != 1 || got[0][0] != "2s" {
		t.Fatal("new physical session did not capture new provider default", got)
	}
}

func TestRelationLockWaitsAbort(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		e := engine.New()
		a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		enginetest.SessionSQL(t, a, "CREATE TABLE u(v INT)")
		enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
		ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(9)")
		done := waitsCommandAsync(e, b, context.Background(), "SELECT v FROM t", prepare)
		enginetest.LockPending(t, e, 1)
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
		rc := enginetest.TransactionBegin(t, c)
		enginetest.SessionSQL(t, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
		if got := enginetest.Tuples(enginetest.SessionSQL(t, c, "SELECT v FROM u")); len(got) != 0 {
			t.Fatal("failed private writes published", got)
		}
		enginetest.TransactionEnd(t, c, rc, false)
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.LockEmpty(t, e)
	}
}

func TestRelationLockWaitsNowaitAbort(t *testing.T) {
	e := engine.New()
	a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE u(v INT)")
	ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(9)")
	op, _, err := b.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, mustLockStatement("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"), 0, nil)
	err = op.CommandError(err)
	op.Done()
	waitsDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`)
	if b.TransactionState() != session.TxAborted {
		t.Fatal("NOWAIT did not abort explicit transaction")
	}
	rc := enginetest.TransactionBegin(t, c)
	enginetest.SessionSQL(t, c, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT")
	if got := enginetest.Tuples(enginetest.SessionSQL(t, c, "SELECT v FROM u")); len(got) != 0 {
		t.Fatal("NOWAIT published prior writes", got)
	}
	enginetest.TransactionEnd(t, c, rc, false)
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.TransactionEnd(t, a, ra, false)
	enginetest.LockEmpty(t, e)
}
