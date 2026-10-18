package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func controlsExecute(t *testing.T, s *Session, q string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	r, err := s.Engine().Execute(op, statement(t, q), 0, nil)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if err == nil && r.Count != 0 {
		t.Fatal(q, r.Count)
	}
	return err
}
func controlsDescribe(t *testing.T, s *Session, q string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	d, err := s.Engine().Describe(op, statement(t, q))
	if err == nil && (d.Parameters.Count != 0 || len(d.Results) != 0) {
		t.Fatal(d)
	}
	return err
}
func controlsRequire(t *testing.T, s *Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}
func controlsAbort(t *testing.T, s *Session) {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	_, err := s.Engine().Execute(op, statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatal(err)
	}
}
func TestTransactionControlsChainIdentity(t *testing.T) {
	for _, q := range []string{"COMMIT AND CHAIN", "END WORK AND CHAIN", "ROLLBACK AND CHAIN", "ABORT TRANSACTION AND CHAIN"} {
		for _, aborted := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/active", true: "/aborted"}[aborted], func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
				controlsRequire(t, a, "BEGIN")
				sessionSQL(t, a, "INSERT INTO t VALUES(1)")
				old, ticket := a.transactions.ref, a.transactions.ticket
				if aborted {
					controlsAbort(t, a)
				}
				if err := controlsDescribe(t, a, q); err != nil {
					t.Fatal(err)
				}
				if a.transactions.ref != old || a.transactions.ticket != map[bool]*relationTicket{false: ticket, true: nil}[aborted] {
					t.Fatal("Describe ended owner")
				}
				controlsRequire(t, a, q)
				next := a.transactions.ref
				fresh := a.transactions.ticket
				if next.ID() == 0 || next.ID() <= old.ID() || next.Epoch() != e.CatalogRead().Epoch() || fresh == nil || fresh == ticket || fresh.Ref() != next || a.TransactionState() != TxActive || a.transactions.command != 0 || a.transactions.writes != nil || len(fresh.resources) != 0 {
					t.Fatal("chain did not create independent empty authority", next, a.transactions)
				}
				e.locks.mu.Lock()
				_, oldLive := e.locks.owners[old]
				e.locks.mu.Unlock()
				if oldLive {
					t.Fatal("old ticket survived")
				}
				op := transactionOperation(t, a)
				_, err := e.Commit(op, old)
				op.Done()
				if err == nil {
					t.Fatal("old reference ended chain")
				}
				sessionSQL(t, a, "INSERT INTO t VALUES(2)")
				want := []int64{}
				if !aborted && (strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")) {
					want = []int64{1}
				}
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
				controlsRequire(t, a, "ROLLBACK AND NO CHAIN")
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
				lockEmpty(t, e)
			})
		}
	}
}
func TestTransactionControlsCountersAndReferences(t *testing.T) {
	t.Run("pre_admission_cancellation", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, b, "CREATE TABLE t(v INT)")
		controlsRequire(t, a, "BEGIN")
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		sessionSQL(t, a, "INSERT INTO t VALUES(1)")
		ref, ticket, command := a.transactions.ref, a.transactions.ticket, a.transactions.command
		ctx, cancel := context.WithCancel(context.Background())
		op, err := a.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		defer op.Done()
		cancel()
		// Synchronize actual propagation into this genuine operation before
		// entering any transaction API; this is not a pre-canceled Acquire.
		<-op.Context().Done()
		assertCanceled := func(err error) {
			t.Helper()
			var diagnostic interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
				t.Fatal("pre-admission context error", err)
			}
			if a.transactions.ref != ref || a.transactions.ticket != ticket ||
				a.transactions.command != command || a.TransactionState() != TxActive {
				t.Fatal("pre-canceled entry changed genuine transaction")
			}
		}
		_, err = e.Begin(op, TxOptions{})
		assertCanceled(err)
		_, err = e.Rollback(op, ref)
		assertCanceled(err)
		scope, err := e.StartStatement(op)
		assertCanceled(err)
		if scope != nil {
			t.Fatal("pre-canceled operation admitted a statement")
		}
		transactionRows(t, a, "SELECT v FROM t", 1)
		waiter := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		lockPending(t, e, 1)
		frontier := e.commitSeq
		// Unlike Begin/Rollback/statement entry, native Commit deliberately
		// admits caller cancellation, then discards a valid incarnation and
		// releases its holds with the original context error.
		result, err := e.Commit(op, ref)
		var diagnostic interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || result.Committed {
			t.Fatal("canceled native Commit outcome", result, err)
		}
		if a.TransactionState() != TxIdle || a.transactions.ref.ID() != 0 ||
			a.transactions.ticket != nil || a.transactions.writes != nil || e.commitSeq != frontier {
			t.Fatal("canceled native Commit retained work/holds or published")
		}
		op.Done()
		lockAwait(t, waiter, nil)
		transactionRows(t, b, "SELECT v FROM t")
		lockEmpty(t, e)
		freshRef := transactionBegin(t, a)
		sessionSQL(t, a, "INSERT INTO t VALUES(2)")
		if !transactionEnd(t, a, freshRef, true).Committed {
			t.Fatal("fresh native transaction did not commit")
		}
		transactionRows(t, b, "SELECT v FROM t", 2)
	})
	for _, counter := range []string{"tx", "frontier", "command"} {
		t.Run(counter, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			old := a.transactions.ref
			frontier := e.commitSeq
			switch counter {
			case "tx":
				a.transactions.next = ids.TxID(^uint64(0))
			case "frontier":
				e.commitSeq = ids.CommitSeq(^uint64(0))
			case "command":
				a.transactions.command = ids.CommandID(^uint64(0))
			}
			err := controlsExecute(t, a, "COMMIT AND CHAIN")
			if counter == "command" {
				if err != nil || a.transactions.command != 0 || a.transactions.ref == old {
					t.Fatal(err, a.transactions)
				}
				transactionRows(t, b, "SELECT v FROM t", 1)
				controlsRequire(t, a, "ROLLBACK")
			} else {
				want := map[string]string{"tx": "stuntgres: transaction identity exhausted", "frontier": "stuntgres: commit sequence exhausted"}[counter]
				var diagnostic interface{ SQLState() string }
				if err == nil || err.Error() != want || errors.As(err, &diagnostic) || a.transactions.ref.ID() != 0 && a.transactions.ref != old {
					t.Fatal(err, a.transactions.ref)
				}
				if counter == "tx" && e.commitSeq != frontier {
					t.Fatal("birth exhaustion published")
				}
				transactionRows(t, b, "SELECT v FROM t")
				controlsRequire(t, a, "ROLLBACK")
			}
		})
	}
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref := a.transactions.ref
	foreignRef := transactionBegin(t, b)
	op := transactionOperation(t, a)
	copyOp := *op
	foreign := transactionOperation(t, b)
	for _, bad := range []*Operation{nil, &copyOp, foreign} {
		if _, err := e.Commit(bad, ref); err == nil {
			t.Fatal("invalid Operation accepted")
		}
	}
	for _, bad := range []ids.TxRef{{}, foreignRef} {
		if _, err := e.Rollback(op, bad); err == nil {
			t.Fatal("invalid ref accepted")
		}
	}
	op.Done()
	if _, err := e.Commit(op, ref); err == nil {
		t.Fatal("done Operation accepted")
	}
	foreign.Done()
	transactionEnd(t, b, foreignRef, false)
	transactionRows(t, a, "SELECT v FROM t", 1)
	controlsRequire(t, a, "COMMIT")
	transactionRows(t, b, "SELECT v FROM t", 1)
}
func TestTransactionControlsLockLifecycle(t *testing.T) {
	for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN", "COMMIT"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			if err := controlsDescribe(t, a, q); err != nil {
				t.Fatal(err)
			}
			done := make(chan Result, 1)
			fail := make(chan error, 1)
			go func() {
				op, err := b.Acquire(context.Background())
				if err != nil {
					fail <- err
					return
				}
				r, err := e.Execute(op, mustLockStatement("SELECT v FROM t ORDER BY v"), 0, nil)
				op.Done()
				if err != nil {
					fail <- err
					return
				}
				done <- r
			}()
			lockPending(t, e, 1)
			controlsRequire(t, a, q)
			var r Result
			select {
			case r = <-done:
			case err := <-fail:
				t.Fatal(err)
			}
			want := 0
			if strings.HasPrefix(q, "COMMIT") {
				want = 1
			}
			if r.Count != int64(want) {
				t.Fatal("waiter resumed before correct publication", r.Count)
			}
			r.Rows.Close()
			if strings.Contains(q, "CHAIN") {
				if len(a.transactions.ticket.resources) != 0 {
					t.Fatal("AX inherited")
				}
				sessionSQL(t, a, "INSERT INTO t VALUES(2)")
				controlsRequire(t, a, "ROLLBACK")
			}
			lockEmpty(t, e)
		})
	}
}
func TestTransactionControlsCancellationAndReplacement(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	sessionSQL(t, b, "INSERT INTO t VALUES(77)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.TransactionState() != TxIdle {
		t.Fatal("precancel birth")
	}
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	controlsRequire(t, b, "BEGIN")
	ctx, cancel = context.WithCancel(context.Background())
	done := lockAsync(e, b, ctx, "INSERT INTO t VALUES(2)", false)
	lockPending(t, e, 1)
	cancel()
	lockAwait(t, done, context.Canceled)
	if b.TransactionState() != TxAborted {
		t.Fatal("cancel did not abort")
	}
	controlsRequire(t, b, "ROLLBACK AND CHAIN")
	controlsRequire(t, b, "ROLLBACK")
	controlsRequire(t, a, "ROLLBACK")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	retained := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 77)
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	op := transactionOperation(t, a)
	_, err := e.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
	op.Done()
	if !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	controlsRequire(t, a, "ROLLBACK AND CHAIN")
	if a.transactions.ref.Epoch() != e.CatalogRead().Epoch() {
		t.Fatal("stale chained epoch")
	}
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "COMMIT")
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 99)
	if retained.count != 2 {
		t.Fatal("replacement invalidated retained rows")
	}
}
func TestTransactionControlsSessionLifecycle(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(77)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	old := a.transactions.ref
	if err := a.Reuse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.transactions.ref != old {
		t.Fatal("healthy reuse ended SQL block")
	}
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 77)
	transactionRows(t, b, "SELECT v FROM t", 77)
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	lockPending(t, e, 1)
	a.RequestEnd(CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	lockAwait(t, done, nil)
	a.RequestEnd(CleanClose)
	transactionRows(t, b, "SELECT v FROM t", 77)
	lockEmpty(t, e)
	c := sessionStart(t, e, nil)
	controlsRequire(t, b, "BEGIN")
	sessionSQL(t, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	controlsRequire(t, c, "BEGIN")
	draining := lockAsync(e, c, context.Background(), "INSERT INTO t VALUES(3)", false)
	lockPending(t, e, 1)
	c.RequestEnd(Terminated)
	lockAwait(t, draining, context.Canceled)
	if err := c.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.TransactionState() != TxActive {
		t.Fatal("terminal drain ended peer")
	}
	controlsRequire(t, b, "COMMIT")
	transactionRows(t, b, "SELECT v FROM t", 77)
	lockEmpty(t, e)
}
func TestTransactionControlsNativeRoundTrip(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(77)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	epoch := e.CatalogRead().Epoch()
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	copyEngine := New()
	if err := copyEngine.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	c := sessionStart(t, copyEngine, nil)
	transactionRows(t, c, "SELECT v FROM t", 77)
	for _, script := range []string{"BEGIN; CREATE TABLE x(v INT); COMMIT;", "COMMIT;", "ROLLBACK;", "CREATE TABLE x(v INT); INSERT INTO x VALUES('bad');"} {
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore succeeded", script)
		}
		if e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed epoch")
		}
	}
	readErr := errors.New("transaction controls reader failure")
	if err := e.Restore(context.Background(), relationFailReader{readErr}); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Restore(canceled, strings.NewReader("CREATE TABLE x(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch {
		t.Fatal("failed I/O/cancel replaced eligible transaction")
	}
	controlsRequire(t, a, "COMMIT")
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 77)
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	transactionRows(t, b, "SELECT v FROM t", 99)
}
func TestTransactionControlsStateMatrix(t *testing.T) {
	queries := []string{"BEGIN", "BEGIN WORK", "BEGIN TRANSACTION", "START TRANSACTION"}
	for _, word := range []string{"COMMIT", "END", "ROLLBACK", "ABORT"} {
		for _, optional := range []string{"", " WORK", " TRANSACTION"} {
			for _, suffix := range []string{"", " AND CHAIN", " AND NO CHAIN"} {
				queries = append(queries, word+optional+suffix)
			}
		}
	}
	for _, q := range queries {
		for _, state := range []TxState{TxIdle, TxActive, TxAborted} {
			for _, prepare := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepare]+"/"+map[TxState]string{TxIdle: "idle", TxActive: "active", TxAborted: "aborted"}[state]+"/"+strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
					e := New()
					a := sessionStart(t, e, nil)
					b := sessionStart(t, e, nil)
					sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
					if state != TxIdle {
						controlsRequire(t, a, "BEGIN")
						sessionSQL(t, a, "INSERT INTO t VALUES(1)")
						if state == TxAborted {
							controlsAbort(t, a)
						}
					}
					ref, command, ticket := a.transactions.ref, a.transactions.command, a.transactions.ticket
					ast := statement(t, q)
					start := strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "START")
					if prepare {
						op := transactionOperation(t, a)
						description, err := e.Describe(op, ast)
						op.Done()
						if state == TxAborted && start {
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "25P02" {
								t.Fatal(err)
							}
							return
						}
						if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 {
							t.Fatal(description, err)
						}
						if a.transactions.ref != ref || a.transactions.command != command || a.transactions.ticket != ticket || a.TransactionState() != state {
							t.Fatal("Describe affected transaction")
						}
					}
					op := transactionOperation(t, a)
					result, err := e.Execute(op, ast, 0, nil)
					op.Done()
					if result.Count != 0 || result.Rows != nil {
						t.Fatal("control result", result)
					}
					if state == TxAborted && start {
						var d *sqlstate.Error
						if !errors.As(err, &d) || d.Code != "25P02" {
							t.Fatal(err)
						}
					} else if !start && state == TxIdle && strings.HasSuffix(q, "AND CHAIN") {
						var d *sqlstate.Error
						if !errors.As(err, &d) || d.Code != "25P01" {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if start && state == TxActive && (a.transactions.ref != ref || a.transactions.command != command || a.transactions.ticket != ticket) {
						t.Fatal("redundant start changed owner")
					}
					want := []int64{}
					if state == TxActive && (strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")) {
						want = []int64{1}
					}
					transactionRows(t, b, "SELECT v FROM t", want...)
				})
			}
		}
	}
}

func TestTransactionControlsMissingOperationAuthority(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref, ticket, command := a.transactions.ref, a.transactions.ticket, a.transactions.command
	op := transactionOperation(t, a)
	nonmember := *op
	nonmember.self = &nonmember
	assertError := func(err error) {
		t.Helper()
		if err == nil || err.Error() != "stuntgres: inactive operation" {
			t.Fatal("nonmember authority", err)
		}
	}
	_, err := e.Begin(&nonmember, TxOptions{})
	assertError(err)
	_, err = e.Commit(&nonmember, ref)
	assertError(err)
	_, err = e.Rollback(&nonmember, ref)
	assertError(err)
	scope, err := e.StartStatement(&nonmember)
	assertError(err)
	if scope != nil {
		t.Fatal("nonmember admitted a scope")
	}
	if a.transactions.ref != ref || a.transactions.ticket != ticket || a.transactions.command != command || a.TransactionState() != TxActive {
		t.Fatal("nonmember mutated genuine owner")
	}
	op.Done()
	transactionRows(t, a, "SELECT v FROM t", 1)
	done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	lockPending(t, e, 1)
	// The real newly acquired end lease still commits the original batch and
	// releases the original hold; a fake identity must not poison its eligibility.
	real := transactionOperation(t, a)
	result, err := e.Commit(real, ref)
	real.Done()
	if err != nil || !result.Committed {
		t.Fatal(result, err)
	}
	lockAwait(t, done, nil)
	transactionRows(t, b, "SELECT v FROM t", 1)
	lockEmpty(t, e)
}
