package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func lockPending(t *testing.T, e *Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		got := 0
		for _, entry := range e.locks.entries {
			got += len(entry.pending)
		}
		e.locks.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %d, want %d", got, n)
		}
		runtime.Gosched()
	}
}
func lockEmpty(t *testing.T, e *Engine) {
	t.Helper()
	e.locks.mu.Lock()
	defer e.locks.mu.Unlock()
	if len(e.locks.owners) != 0 || len(e.locks.entries) != 0 {
		t.Fatalf("residual lock state: owners=%d entries=%d", len(e.locks.owners), len(e.locks.entries))
	}
}
func lockAsync(e *Engine, s *Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
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
			op.Done()
		}
		done <- err
	}()
	return done
}
func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}
func lockAwait(t *testing.T, done <-chan error, want error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lock operation did not terminate")
	}
}
func TestRelationLockCompatibility(t *testing.T) {
	modes := []relationLockMode{accessShare, rowExclusive, accessExclusive, shareUpdateExclusive, shareRowExclusive}
	for _, held := range modes {
		for _, request := range modes {
			t.Run(fmt.Sprintf("%d/%d", held, request), func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := transactionBegin(t, a)
				rb := transactionBegin(t, b)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				if err := e.locks.acquire(context.Background(), a.transactions.ticket, lockResource{ObjectRef: ref}, held); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					done <- e.locks.acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: ref}, request)
				}()
				// PostgreSQL Table 13.2: SRX permits readers but excludes RX,
				// SUE and other SRX holders; SUE otherwise permits RX.
				compatible := held != accessExclusive && request != accessExclusive &&
					!(held == shareUpdateExclusive && request == shareUpdateExclusive) &&
					!(held == shareRowExclusive && request != accessShare) &&
					!(request == shareRowExclusive && held != accessShare)
				if compatible {
					lockAwait(t, done, nil)
				} else {
					lockPending(t, e, 1)
					transactionEnd(t, a, ra, false)
					lockAwait(t, done, nil)
				}
				transactionEnd(t, b, rb, false)
				if compatible {
					transactionEnd(t, a, ra, false)
				}
				lockEmpty(t, e)
			})
		}
	}
	t.Run("self reentry and exact tickets", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		rb := transactionBegin(t, b)
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		ticket := a.transactions.ticket
		if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: ref}, accessExclusive); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- e.locks.acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: ref}, accessExclusive)
		}()
		lockPending(t, e, 1)
		for _, mode := range modes {
			if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
		}
		copy := *ticket
		if err := e.locks.acquire(context.Background(), &copy, lockResource{ObjectRef: ref}, accessShare); err == nil {
			t.Fatal("copied ticket accepted")
		}
		for _, mode := range []relationLockMode{0, rowUpdate, accessShare | rowExclusive, shareRowExclusive | accessShare} {
			if err := e.locks.acquire(context.Background(), ticket, lockResource{ObjectRef: ref}, mode); err == nil {
				t.Fatal("invalid mode accepted")
			}
		}
		transactionEnd(t, a, ra, false)
		lockAwait(t, done, nil)
		transactionEnd(t, b, rb, false)
		lockEmpty(t, e)
	})
}
func TestRelationLockCancellation(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	c := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	rc := transactionBegin(t, c)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	if err := e.locks.acquire(context.Background(), a.transactions.ticket, lockResource{ObjectRef: ref}, accessShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ax := make(chan error, 1)
	go func() {
		ax <- e.locks.acquire(ctx, b.transactions.ticket, lockResource{ObjectRef: ref}, accessExclusive)
	}()
	lockPending(t, e, 1)
	rx := make(chan error, 1)
	go func() {
		rx <- e.locks.acquire(context.Background(), c.transactions.ticket, lockResource{ObjectRef: ref}, rowExclusive)
	}()
	lockPending(t, e, 2)
	cancel()
	lockAwait(t, ax, context.Canceled)
	lockAwait(t, rx, nil)
	lockPending(t, e, 0)
	transactionEnd(t, b, rb, false)
	transactionEnd(t, c, rc, false)
	transactionEnd(t, a, ra, false)
	lockEmpty(t, e)
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			ra := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			var rb ids.TxRef
			if explicit {
				rb = transactionBegin(t, b)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := lockAsync(e, b, ctx, "SELECT v FROM t", false)
			lockPending(t, e, 1)
			if got := b.TransactionState(); got != map[bool]TxState{false: TxImplicit, true: TxActive}[explicit] {
				t.Fatal("state observation blocked or wrong", got)
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			lockPending(t, e, 0)
			if explicit {
				if b.TransactionState() != TxAborted {
					t.Fatal("not aborted")
				}
				transactionEnd(t, b, rb, false)
			} else if b.TransactionState() != TxIdle {
				t.Fatal("not idle")
			}
			transactionEnd(t, a, ra, false)
			transactionRows(t, b, "SELECT v FROM t")
			lockEmpty(t, e)
		})
	}
}
func TestRelationLockVisibilityAndPreparation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepare := range []bool{false, true} {
			for _, query := range []string{"SELECT v FROM t ORDER BY v", "INSERT INTO t VALUES(2)"} {
				t.Run(fmt.Sprintf("%t/%t/%s", commit, prepare, query), func(t *testing.T) {
					e := New()
					a := sessionStart(t, e, nil)
					b := sessionStart(t, e, nil)
					sessionSQL(t, a, "CREATE TABLE t(v INT)")
					sessionSQL(t, a, "INSERT INTO t VALUES(7)")
					ra := transactionBegin(t, a)
					sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					sessionSQL(t, a, "INSERT INTO t VALUES(1)")
					done := lockAsync(e, b, context.Background(), query, prepare)
					lockPending(t, e, 1)
					transactionEnd(t, a, ra, commit)
					lockAwait(t, done, nil)
					if prepare && strings.HasPrefix(query, "INSERT") {
						sessionSQL(t, b, query)
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
					transactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
					lockEmpty(t, e)
				})
			}
		}
	}
	for _, query := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		t.Run("explicit prepare "+query, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra := transactionBegin(t, a)
			op := transactionOperation(t, a)
			_, err := e.Describe(op, statement(t, query))
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			rb := transactionBegin(t, b)
			done := lockAsync(e, b, context.Background(), "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false)
			lockPending(t, e, 1)
			transactionEnd(t, a, ra, false)
			lockAwait(t, done, nil)
			transactionEnd(t, b, rb, false)
			lockEmpty(t, e)
		})
	}
}
func TestRelationLockLifecycle(t *testing.T) {
	for _, failure := range []string{"SELECT * FROM absent", "INSERT INTO t VALUES(NULL)", "SELECT * FROM t WHERE v = )"} {
		t.Run(failure, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT NOT NULL)")
			ra := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			op := transactionOperation(t, a)
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
			if a.TransactionState() != TxAborted {
				t.Fatal("not aborted")
			}
			transactionRows(t, b, "SELECT v FROM t")
			lockEmpty(t, e)
			transactionEnd(t, a, ra, false)
		})
	}
	t.Run("physical end holder", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		lockPending(t, e, 1)
		a.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		lockAwait(t, done, nil)
		lockEmpty(t, e)
	})
	t.Run("invalid end preserves owner", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		bad := ra
		bad = withTxID(bad, bad.ID()+1)
		op := transactionOperation(t, a)
		if _, err := e.Rollback(op, bad); err == nil {
			t.Fatal("stale ref accepted")
		}
		op.Done()
		e.locks.mu.Lock()
		valid := e.locks.valid(a.transactions.ticket)
		e.locks.mu.Unlock()
		if !valid {
			t.Fatal("invalid end released owner")
		}
		transactionEnd(t, a, ra, false)
		lockEmpty(t, e)
	})
}
func TestRelationLockReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			ra := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
			lockPending(t, e, 1)
			var err error
			if restore {
				err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(9);"))
			} else {
				err = e.Reset(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			lockAwait(t, done, ErrCatalogReplaced)
			lockEmpty(t, e)
			transactionEnd(t, a, ra, false)
			if restore {
				transactionRows(t, b, "SELECT v FROM t", 9)
			} else {
				sessionSQL(t, b, "CREATE TABLE t(v INT)")
				transactionRows(t, b, "SELECT v FROM t")
			}
			lockEmpty(t, e)
		})
	}
	t.Run("failed restore preserves holds", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		epoch := e.CatalogRead().Epoch()
		if err := e.Restore(context.Background(), strings.NewReader("INSERT INTO absent VALUES(1)")); err == nil {
			t.Fatal("expected failure")
		}
		if e.CatalogRead().Epoch() != epoch {
			t.Fatal("epoch changed")
		}
		done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		lockPending(t, e, 1)
		transactionEnd(t, a, ra, true)
		lockAwait(t, done, nil)
		lockEmpty(t, e)
	})
}
func TestRelationLockDiagnostics(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	op := transactionOperation(t, s)
	_, err := e.Describe(op, statement(t, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE"))
	op.Done()
	if err != nil {
		t.Fatal("utility prepare performed semantics", err)
	}
	lockEmpty(t, e)
	check := func(q, code, message string) {
		t.Helper()
		op := transactionOperation(t, s)
		_, err := e.Execute(op, statement(t, q), 0, nil)
		op.Done()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Detail != "" || d.Hint != "" {
			t.Fatalf("diagnostic %#v / %v", d, err)
		}
	}
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks")
	ref := transactionBegin(t, s)
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "42P01", "relation \"absent\" does not exist")
	check("LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	transactionEnd(t, s, ref, false)
	lockEmpty(t, e)
}
