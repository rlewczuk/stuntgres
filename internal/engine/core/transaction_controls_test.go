package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func controlsExecute(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	r, err := engine.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if err == nil && r.Count != 0 {
		t.Fatal(q, r.Count)
	}
	return err
}
func controlsDescribe(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	d, err := engine.EngineOf(s).Describe(op, enginetest.Statement(t, q))
	if err == nil && (d.Parameters.Count != 0 || len(d.Results) != 0) {
		t.Fatal(d)
	}
	return err
}
func controlsRequire(t *testing.T, s *session.Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}
func controlsAbort(t *testing.T, s *session.Session) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	_, err := engine.EngineOf(s).Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatal(err)
	}
}
func TestTransactionControlsChainIdentity(t *testing.T) {
	for _, q := range []string{"COMMIT AND CHAIN", "END WORK AND CHAIN", "ROLLBACK AND CHAIN", "ABORT TRANSACTION AND CHAIN"} {
		for _, aborted := range []bool{false, true} {
			t.Run(q+map[bool]string{false: "/active", true: "/aborted"}[aborted], func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
				controlsRequire(t, a, "BEGIN")
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
				old, ticket := a.Transactions.Ref, a.Transactions.Ticket
				if aborted {
					controlsAbort(t, a)
				}
				if err := controlsDescribe(t, a, q); err != nil {
					t.Fatal(err)
				}
				if a.Transactions.Ref != old || a.Transactions.Ticket != map[bool]*lock.Ticket{false: ticket, true: nil}[aborted] {
					t.Fatal("Describe ended owner")
				}
				controlsRequire(t, a, q)
				next := a.Transactions.Ref
				fresh := a.Transactions.Ticket
				if next.ID() == 0 || next.ID() <= old.ID() || next.Epoch() != e.CatalogRead().Epoch() || fresh == nil || fresh == ticket || fresh.Ref() != next || a.TransactionState() != session.TxActive || a.Transactions.Command != 0 || a.Transactions.Writes != nil || len(e.Locks.Inspect(fresh).Journals[fresh]) != 0 {
					t.Fatal("chain did not create independent empty authority", next, a.Transactions)
				}
				lockState := e.Locks.Inspect()
				_, oldLive := lockState.Owners[old]
				if oldLive {
					t.Fatal("old ticket survived")
				}
				op := enginetest.TransactionOperation(t, a)
				_, err := e.Commit(op, old)
				op.Done()
				if err == nil {
					t.Fatal("old reference ended chain")
				}
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
				want := []int64{}
				if !aborted && (strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")) {
					want = []int64{1}
				}
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
				controlsRequire(t, a, "ROLLBACK AND NO CHAIN")
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", want...)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
func TestTransactionControlsCountersAndReferences(t *testing.T) {
	t.Run("pre_admission_cancellation", func(t *testing.T) {
		e := engine.New()
		a := enginetest.StartSession(t, e, nil)
		b := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
		controlsRequire(t, a, "BEGIN")
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
		ref, ticket, command := a.Transactions.Ref, a.Transactions.Ticket, a.Transactions.Command
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
			if a.Transactions.Ref != ref || a.Transactions.Ticket != ticket ||
				a.Transactions.Command != command || a.TransactionState() != session.TxActive {
				t.Fatal("pre-canceled entry changed genuine transaction")
			}
		}
		_, err = e.Begin(op, session.TxOptions{})
		assertCanceled(err)
		_, err = e.Rollback(op, ref)
		assertCanceled(err)
		scope, err := e.StartStatement(op)
		assertCanceled(err)
		if scope != nil {
			t.Fatal("pre-canceled operation admitted a statement")
		}
		enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
		waiter := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		enginetest.LockPending(t, e, 1)
		frontier := e.Visibility.CommitSeq()
		// Unlike Begin/Rollback/statement entry, native Commit deliberately
		// admits caller cancellation, then discards a valid incarnation and
		// releases its holds with the original context error.
		result, err := e.Commit(op, ref)
		var diagnostic interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || result.Committed {
			t.Fatal("canceled native Commit outcome", result, err)
		}
		if a.TransactionState() != session.TxIdle || a.Transactions.Ref.ID() != 0 ||
			a.Transactions.Ticket != nil || a.Transactions.Writes != nil || e.Visibility.CommitSeq() != frontier {
			t.Fatal("canceled native Commit retained work/holds or published")
		}
		op.Done()
		enginetest.LockAwait(t, waiter, nil)
		enginetest.TransactionRows(t, b, "SELECT v FROM t")
		enginetest.LockEmpty(t, e)
		freshRef := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
		if !enginetest.TransactionEnd(t, a, freshRef, true).Committed {
			t.Fatal("fresh native transaction did not commit")
		}
		enginetest.TransactionRows(t, b, "SELECT v FROM t", 2)
	})
	for _, counter := range []string{"tx", "frontier", "command"} {
		t.Run(counter, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			controlsRequire(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			old := a.Transactions.Ref
			frontier := e.Visibility.CommitSeq()
			switch counter {
			case "tx":
				a.Transactions.Next = ids.TxID(^uint64(0))
			case "frontier":
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
			case "command":
				a.Transactions.Command = ids.CommandID(^uint64(0))
			}
			err := controlsExecute(t, a, "COMMIT AND CHAIN")
			if counter == "command" {
				if err != nil || a.Transactions.Command != 0 || a.Transactions.Ref == old {
					t.Fatal(err, a.Transactions)
				}
				enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
				controlsRequire(t, a, "ROLLBACK")
			} else {
				want := map[string]string{"tx": "stuntgres: transaction identity exhausted", "frontier": "stuntgres: commit sequence exhausted"}[counter]
				var diagnostic interface{ SQLState() string }
				if err == nil || err.Error() != want || errors.As(err, &diagnostic) || a.Transactions.Ref.ID() != 0 && a.Transactions.Ref != old {
					t.Fatal(err, a.Transactions.Ref)
				}
				if counter == "tx" && e.Visibility.CommitSeq() != frontier {
					t.Fatal("birth exhaustion published")
				}
				enginetest.TransactionRows(t, b, "SELECT v FROM t")
				controlsRequire(t, a, "ROLLBACK")
			}
		})
	}
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref := a.Transactions.Ref
	foreignRef := enginetest.TransactionBegin(t, b)
	op := enginetest.TransactionOperation(t, a)
	copyOp := *op
	foreign := enginetest.TransactionOperation(t, b)
	for _, bad := range []*session.Operation{nil, &copyOp, foreign} {
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
	enginetest.TransactionEnd(t, b, foreignRef, false)
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	controlsRequire(t, a, "COMMIT")
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
}
func TestTransactionControlsLockLifecycle(t *testing.T) {
	for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN", "COMMIT"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			if err := controlsDescribe(t, a, q); err != nil {
				t.Fatal(err)
			}
			done := make(chan engine.Result, 1)
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
			enginetest.LockPending(t, e, 1)
			controlsRequire(t, a, q)
			var r engine.Result
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
				if len(e.Locks.Inspect(a.Transactions.Ticket).Journals[a.Transactions.Ticket]) != 0 {
					t.Fatal("AX inherited")
				}
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
				controlsRequire(t, a, "ROLLBACK")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
func TestTransactionControlsCancellationAndReplacement(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(77)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.TransactionState() != session.TxIdle {
		t.Fatal("precancel birth")
	}
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	controlsRequire(t, b, "BEGIN")
	ctx, cancel = context.WithCancel(context.Background())
	done := enginetest.LockAsync(e, b, ctx, "INSERT INTO t VALUES(2)", false)
	enginetest.LockPending(t, e, 1)
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	if b.TransactionState() != session.TxAborted {
		t.Fatal("cancel did not abort")
	}
	controlsRequire(t, b, "ROLLBACK AND CHAIN")
	controlsRequire(t, b, "ROLLBACK")
	controlsRequire(t, a, "ROLLBACK")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	retained := enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 77)
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	op := enginetest.TransactionOperation(t, a)
	_, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM t"), 0, nil)
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	controlsRequire(t, a, "ROLLBACK AND CHAIN")
	if a.Transactions.Ref.Epoch() != e.CatalogRead().Epoch() {
		t.Fatal("stale chained epoch")
	}
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "COMMIT")
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 2, 99)
	if retained.Count != 2 {
		t.Fatal("replacement invalidated retained rows")
	}
}
func TestTransactionControlsSessionLifecycle(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(77)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	old := a.Transactions.Ref
	if err := a.Reuse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Transactions.Ref != old {
		t.Fatal("healthy reuse ended SQL block")
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 77)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 77)
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	enginetest.LockPending(t, e, 1)
	a.RequestEnd(session.CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	enginetest.LockAwait(t, done, nil)
	a.RequestEnd(session.CleanClose)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 77)
	enginetest.LockEmpty(t, e)
	c := enginetest.StartSession(t, e, nil)
	controlsRequire(t, b, "BEGIN")
	enginetest.SessionSQL(t, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	controlsRequire(t, c, "BEGIN")
	draining := enginetest.LockAsync(e, c, context.Background(), "INSERT INTO t VALUES(3)", false)
	enginetest.LockPending(t, e, 1)
	c.RequestEnd(session.Terminated)
	enginetest.LockAwait(t, draining, context.Canceled)
	if err := c.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.TransactionState() != session.TxActive {
		t.Fatal("terminal drain ended peer")
	}
	controlsRequire(t, b, "COMMIT")
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 77)
	enginetest.LockEmpty(t, e)
}
func TestTransactionControlsNativeRoundTrip(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(77)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	epoch := e.CatalogRead().Epoch()
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	copyEngine := engine.New()
	if err := engine.Restore(context.Background(), copyEngine, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	c := enginetest.StartSession(t, copyEngine, nil)
	enginetest.TransactionRows(t, c, "SELECT v FROM t", 77)
	for _, script := range []string{"BEGIN; CREATE TABLE x(v INT); COMMIT;", "COMMIT;", "ROLLBACK;", "CREATE TABLE x(v INT); INSERT INTO x VALUES('bad');"} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore succeeded", script)
		}
		if e.CatalogRead().Epoch() != epoch {
			t.Fatal("failed restore changed epoch")
		}
	}
	readErr := errors.New("transaction controls reader failure")
	if err := engine.Restore(context.Background(), e, relationFailReader{readErr}); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(canceled, e, strings.NewReader("CREATE TABLE x(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch {
		t.Fatal("failed I/O/cancel replaced eligible transaction")
	}
	controlsRequire(t, a, "COMMIT")
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 77)
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 99)
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
		for _, state := range []session.TxState{session.TxIdle, session.TxActive, session.TxAborted} {
			for _, prepare := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepare]+"/"+map[session.TxState]string{session.TxIdle: "idle", session.TxActive: "active", session.TxAborted: "aborted"}[state]+"/"+strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
					e := engine.New()
					a := enginetest.StartSession(t, e, nil)
					b := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
					if state != session.TxIdle {
						controlsRequire(t, a, "BEGIN")
						enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
						if state == session.TxAborted {
							controlsAbort(t, a)
						}
					}
					ref, command, ticket := a.Transactions.Ref, a.Transactions.Command, a.Transactions.Ticket
					ast := enginetest.Statement(t, q)
					start := strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "START")
					if prepare {
						op := enginetest.TransactionOperation(t, a)
						description, err := e.Describe(op, ast)
						op.Done()
						if state == session.TxAborted && start {
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "25P02" {
								t.Fatal(err)
							}
							return
						}
						if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 {
							t.Fatal(description, err)
						}
						if a.Transactions.Ref != ref || a.Transactions.Command != command || a.Transactions.Ticket != ticket || a.TransactionState() != state {
							t.Fatal("Describe affected transaction")
						}
					}
					op := enginetest.TransactionOperation(t, a)
					result, err := e.Execute(op, ast, 0, nil)
					op.Done()
					if result.Count != 0 || result.Rows != nil {
						t.Fatal("control result", result)
					}
					if state == session.TxAborted && start {
						var d *sqlstate.Error
						if !errors.As(err, &d) || d.Code != "25P02" {
							t.Fatal(err)
						}
					} else if !start && state == session.TxIdle && strings.HasSuffix(q, "AND CHAIN") {
						var d *sqlstate.Error
						if !errors.As(err, &d) || d.Code != "25P01" {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if start && state == session.TxActive && (a.Transactions.Ref != ref || a.Transactions.Command != command || a.Transactions.Ticket != ticket) {
						t.Fatal("redundant start changed owner")
					}
					want := []int64{}
					if state == session.TxActive && (strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")) {
						want = []int64{1}
					}
					enginetest.TransactionRows(t, b, "SELECT v FROM t", want...)
				})
			}
		}
	}
}

func TestTransactionControlsMissingOperationAuthority(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	ref, ticket, command := a.Transactions.Ref, a.Transactions.Ticket, a.Transactions.Command
	op := enginetest.TransactionOperation(t, a)
	nonmember := *op
	nonmember.Self = &nonmember
	assertError := func(err error) {
		t.Helper()
		if err == nil || err.Error() != "stuntgres: inactive operation" {
			t.Fatal("nonmember authority", err)
		}
	}
	_, err := e.Begin(&nonmember, session.TxOptions{})
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
	if a.Transactions.Ref != ref || a.Transactions.Ticket != ticket || a.Transactions.Command != command || a.TransactionState() != session.TxActive {
		t.Fatal("nonmember mutated genuine owner")
	}
	op.Done()
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	done := enginetest.LockAsync(e, b, context.Background(), "SELECT v FROM t", false)
	enginetest.LockPending(t, e, 1)
	// The real newly acquired end lease still commits the original batch and
	// releases the original hold; a fake identity must not poison its eligibility.
	real := enginetest.TransactionOperation(t, a)
	result, err := e.Commit(real, ref)
	real.Done()
	if err != nil || !result.Committed {
		t.Fatal(result, err)
	}
	enginetest.LockAwait(t, done, nil)
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
	enginetest.LockEmpty(t, e)
}

func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}

type relationFailReader struct{ err error }

func (r relationFailReader) Read([]byte) (int, error) { return 0, r.err }
