package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}
func TestRelationLockCompatibility(t *testing.T) {
	modes := []lock.Mode{lock.AccessShare, lock.RowExclusive, lock.AccessExclusive, lock.ShareUpdateExclusive, lock.ShareRowExclusive}
	for _, held := range modes {
		for _, request := range modes {
			t.Run(fmt.Sprintf("%d/%d", held, request), func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := enginetest.TransactionBegin(t, a)
				rb := enginetest.TransactionBegin(t, b)
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, held); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					done <- e.Locks.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, request)
				}()
				// PostgreSQL Table 13.2: SRX permits readers but excludes RX,
				// SUE and other SRX holders; SUE otherwise permits RX.
				compatible := held != lock.AccessExclusive && request != lock.AccessExclusive &&
					!(held == lock.ShareUpdateExclusive && request == lock.ShareUpdateExclusive) &&
					!(held == lock.ShareRowExclusive && request != lock.AccessShare) &&
					!(request == lock.ShareRowExclusive && held != lock.AccessShare)
				if compatible {
					enginetest.LockAwait(t, done, nil)
				} else {
					enginetest.LockPending(t, e, 1)
					enginetest.TransactionEnd(t, a, ra, false)
					enginetest.LockAwait(t, done, nil)
				}
				enginetest.TransactionEnd(t, b, rb, false)
				if compatible {
					enginetest.TransactionEnd(t, a, ra, false)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
	t.Run("self reentry and exact tickets", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := enginetest.TransactionBegin(t, a)
		rb := enginetest.TransactionBegin(t, b)
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		ticket := a.Transactions.Ticket
		if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- e.Locks.Acquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive)
		}()
		enginetest.LockPending(t, e, 1)
		for _, mode := range modes {
			if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
		}
		copy := *ticket
		if err := e.Locks.Acquire(context.Background(), &copy, lock.Resource{ObjectRef: ref}, lock.AccessShare); err == nil {
			t.Fatal("copied ticket accepted")
		}
		for _, mode := range []lock.Mode{0, lock.ForUpdate, lock.AccessShare | lock.RowExclusive, lock.ShareRowExclusive | lock.AccessShare} {
			if err := e.Locks.Acquire(context.Background(), ticket, lock.Resource{ObjectRef: ref}, mode); err == nil {
				t.Fatal("invalid mode accepted")
			}
		}
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.LockAwait(t, done, nil)
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.LockEmpty(t, e)
	})
}
func TestRelationLockCancellation(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	c := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	rc := enginetest.TransactionBegin(t, c)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	if err := e.Locks.Acquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ax := make(chan error, 1)
	go func() {
		ax <- e.Locks.Acquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive)
	}()
	enginetest.LockPending(t, e, 1)
	rx := make(chan error, 1)
	go func() {
		rx <- e.Locks.Acquire(context.Background(), c.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive)
	}()
	enginetest.LockPending(t, e, 2)
	cancel()
	enginetest.LockAwait(t, ax, context.Canceled)
	enginetest.LockAwait(t, rx, nil)
	enginetest.LockPending(t, e, 0)
	enginetest.TransactionEnd(t, b, rb, false)
	enginetest.TransactionEnd(t, c, rc, false)
	enginetest.TransactionEnd(t, a, ra, false)
	enginetest.LockEmpty(t, e)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			ra := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			var rb ids.TxRef
			if explicit {
				rb = enginetest.TransactionBegin(t, b)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := enginetest.LockAsync(e, b, ctx, "SELECT v FROM t", false)
			enginetest.LockPending(t, e, 1)
			if got := b.TransactionState(); got != map[bool]session.TxState{false: session.TxImplicit, true: session.TxActive}[explicit] {
				t.Fatal("state observation blocked or wrong", got)
			}
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
			enginetest.LockPending(t, e, 0)
			if explicit {
				if b.TransactionState() != session.TxAborted {
					t.Fatal("not aborted")
				}
				enginetest.TransactionEnd(t, b, rb, false)
			} else if b.TransactionState() != session.TxIdle {
				t.Fatal("not idle")
			}
			enginetest.TransactionEnd(t, a, ra, false)
			enginetest.TransactionRows(t, b, "SELECT v FROM t")
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRelationLockVisibilityAndPreparation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepare := range []bool{false, true} {
			for _, query := range []string{"SELECT v FROM t ORDER BY v", "INSERT INTO t VALUES(2)"} {
				t.Run(fmt.Sprintf("%t/%t/%s", commit, prepare, query), func(t *testing.T) {
					e := engine.New()
					a := enginetest.StartSession(t, e, nil)
					b := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
					enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
					ra := enginetest.TransactionBegin(t, a)
					enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
					done := enginetest.LockAsync(e, b, context.Background(), query, prepare)
					enginetest.LockPending(t, e, 1)
					enginetest.TransactionEnd(t, a, ra, commit)
					enginetest.LockAwait(t, done, nil)
					if prepare && strings.HasPrefix(query, "INSERT") {
						enginetest.SessionSQL(t, b, query)
					}
					want := []int64{7}
					if commit {
						want = []int64{1, 7}
					}
					if strings.HasPrefix(query, "INSERT") {
						if commit {
							want = []int64{1, 2, 7}
						} else {
							want = []int64{2, 7}
						}
					}
					enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
	for _, query := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		t.Run("explicit prepare "+query, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra := enginetest.TransactionBegin(t, a)
			op := enginetest.TransactionOperation(t, a)
			_, err := e.Describe(op, enginetest.Statement(t, query))
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			rb := enginetest.TransactionBegin(t, b)
			done := enginetest.LockAsync(e, b, context.Background(), "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, ra, false)
			enginetest.LockAwait(t, done, nil)
			enginetest.TransactionEnd(t, b, rb, false)
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestRelationLockLifecycle(t *testing.T) {
	for _, failure := range []string{"SELECT * FROM absent", "INSERT INTO t VALUES(NULL)", "SELECT * FROM t WHERE v = )"} {
		t.Run(failure, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT NOT NULL)")
			ra := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			op := enginetest.TransactionOperation(t, a)
			stmts, err := parser.Parse(failure)
			scope, startErr := e.StartStatement(op)
			if startErr != nil {
				t.Fatal(startErr)
			}
			if err == nil {
				_, err = scope.Execute(stmts[0], 0, nil)
			}
			err = scope.Finish(err)
			op.Done()
			if err == nil {
				t.Fatal("expected failure")
			}
			if a.TransactionState() != session.TxAborted {
				t.Fatal("not aborted")
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t")
			enginetest.LockEmpty(t, e)
			enginetest.TransactionEnd(t, a, ra, false)
		})
	}
	t.Run("physical end holder", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		enginetest.LockPending(t, e, 1)
		a.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		enginetest.LockAwait(t, done, nil)
		enginetest.LockEmpty(t, e)
	})
	t.Run("invalid end preserves owner", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		bad := ra
		bad = withTxID(bad, bad.ID()+1)
		op := enginetest.TransactionOperation(t, a)
		if _, err := e.Rollback(op, bad); err == nil {
			t.Fatal("stale ref accepted")
		}
		op.Done()
		lockState := e.Locks.Inspect()
		valid := lockState.Valid(a.Transactions.Ticket)
		if !valid {
			t.Fatal("invalid end released owner")
		}
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.LockEmpty(t, e)
	})
}
func TestRelationLockReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
			enginetest.LockPending(t, e, 1)
			var err error
			if restore {
				err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(9);"))
			} else {
				err = engine.Reset(context.Background(), e)
			}
			if err != nil {
				t.Fatal(err)
			}
			enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
			enginetest.LockEmpty(t, e)
			enginetest.TransactionEnd(t, a, ra, false)
			if restore {
				enginetest.TransactionRows(t, b, "SELECT v FROM t", 9)
			} else {
				enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
				enginetest.TransactionRows(t, b, "SELECT v FROM t")
			}
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("failed restore preserves holds", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		epoch := e.CatalogRead().Epoch()
		if err := engine.Restore(context.Background(), e, strings.NewReader("INSERT INTO absent VALUES(1)")); err == nil {
			t.Fatal("expected failure")
		}
		if e.CatalogRead().Epoch() != epoch {
			t.Fatal("epoch changed")
		}
		done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		enginetest.LockPending(t, e, 1)
		enginetest.TransactionEnd(t, a, ra, true)
		enginetest.LockAwait(t, done, nil)
		enginetest.LockEmpty(t, e)
	})
}
func TestRelationLockDiagnostics(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	op := enginetest.TransactionOperation(t, s)
	_, err := e.Describe(op, enginetest.Statement(t, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE"))
	op.Done()
	if err != nil {
		t.Fatal("utility prepare performed semantics", err)
	}
	enginetest.LockEmpty(t, e)
	check := func(q, code, message string) {
		t.Helper()
		op := enginetest.TransactionOperation(t, s)
		_, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
		op.Done()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Detail != "" || d.Hint != "" {
			t.Fatalf("diagnostic %#v / %v", d, err)
		}
	}
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks")
	ref := enginetest.TransactionBegin(t, s)
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "42P01", "relation \"absent\" does not exist")
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	enginetest.TransactionEnd(t, s, ref, false)
	enginetest.LockEmpty(t, e)
}
