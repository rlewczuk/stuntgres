package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

type batchUnitMessage struct {
	scope  *StatementScope
	op     *Operation
	ended  bool
	result error
}

func batchUnitStart(t *testing.T, s *Session, ctx context.Context) *batchUnitMessage {
	t.Helper()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.Engine().StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return &batchUnitMessage{scope: scope, op: op}
}
func (m *batchUnitMessage) step(stmt parser.Statement) (Result, error) {
	if m.scope.finished {
		next, err := m.scope.NextStatement()
		if err != nil {
			return Result{}, err
		}
		m.scope = next
	}
	result, err := m.scope.Execute(stmt, 0, nil)
	finish := m.scope.Finish(err)
	if err == nil {
		err = finish
	}
	return result, err
}
func (m *batchUnitMessage) end(err error) error {
	if !m.ended {
		m.result = m.scope.FinishMessage(err)
		m.ended = true
		m.op.Done()
	}
	return m.result
}
func TestDriverBatchOwnerPromotionAndAdmission(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	m := batchUnitStart(t, a, context.Background())
	defer m.end(context.Canceled)
	if _, err := m.step(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
		t.Fatal(err)
	}
	ref, ticket, start := a.transactions.ref, a.transactions.ticket, a.transactions.start
	if a.TransactionState() != TxImplicit || ref == (TxRef{}) || ticket == nil || a.transactions.command != 1 {
		t.Fatal("retained implicit owner")
	}
	if a.transactions.admission.TryLock() {
		a.transactions.admission.Unlock()
		t.Fatal("message released admission between statements")
	}
	transactionRows(t, b, "SELECT v FROM t")
	first := m.scope
	copy := *first
	if _, err := copy.NextStatement(); err == nil {
		t.Fatal("copied scope authority")
	}
	originalOperation := m.scope.op
	copiedOperation := *originalOperation
	m.scope.op = &copiedOperation
	_, foreignErr := m.scope.NextStatement()
	m.scope.op = originalOperation
	if foreignErr == nil {
		t.Fatal("copied operation acquired message admission")
	}
	if _, err := m.step(statement(t, "BEGIN")); err != nil {
		t.Fatal(err)
	}
	if a.transactions.ref != ref || a.transactions.ticket != ticket || a.transactions.start != start || a.TransactionState() != TxActive {
		t.Fatal("promotion replaced authority")
	}
	if err := first.FinishMessage(nil); err == nil {
		t.Fatal("stale scope ended current message")
	}
	if _, err := m.step(statement(t, "INSERT INTO t VALUES(2)")); err != nil {
		t.Fatal(err)
	}
	final := m.scope
	if err := m.end(nil); err != nil {
		t.Fatal(err)
	}
	if err := final.FinishMessage(errors.New("ignored")); err != nil {
		t.Fatal("idempotent message end", err)
	}
	if _, err := final.NextStatement(); err == nil {
		t.Fatal("ended message admitted work")
	}
	if a.transactions.retainedMessage || a.TransactionState() != TxActive {
		t.Fatal("message end incorrectly ended explicit block")
	}
	transactionRows(t, b, "SELECT v FROM t")
	transactionEnd(t, a, ref, false)
	transactionRows(t, b, "SELECT v FROM t")
}
func TestDriverBatchQueuedPrefixCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE prefix(v INT)")
			sessionSQL(t, b, "CREATE TABLE blocked(v INT)")
			holder := transactionBegin(t, b)
			sessionSQL(t, b, "LOCK TABLE blocked IN ACCESS EXCLUSIVE MODE")
			if explicit {
				transactionBegin(t, a)
			}
			ctx, cancel := context.WithCancel(context.Background())
			m := batchUnitStart(t, a, ctx)
			joined := make(chan struct{})
			result := make(chan error, 1)
			started := false
			t.Cleanup(func() {
				cancel()
				if started {
					select {
					case <-joined:
					case <-time.After(5 * time.Second):
						t.Error("canceled message did not join")
					}
				} else {
					m.end(context.Canceled)
				}
			})
			if _, err := m.step(statement(t, "INSERT INTO prefix VALUES(1)")); err != nil {
				t.Fatal(err)
			}
			address := catalogRef(t, e, e.CatalogRead(), "prefix").Address
			e.mu.Lock()
			published := len(e.storage[address].versions)
			e.mu.Unlock()
			if published != 0 || len(a.transactions.writes[address]) != 1 {
				t.Fatal("prefix was not staged before wait")
			}
			ref, ticket := a.transactions.ref, a.transactions.ticket
			stmt := statement(t, "INSERT INTO blocked VALUES(2)")
			started = true
			go func() { _, err := m.step(stmt); err = m.end(err); result <- err; close(joined) }()
			lockPending(t, e, 1)
			a.transactions.mu.Lock()
			same := a.transactions.ref == ref && a.transactions.ticket == ticket && a.transactions.command == 1
			a.transactions.mu.Unlock()
			if !same {
				t.Fatal("queued command changed prefix owner")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary admitted cancellation", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("message failed to cancel while lock remained held")
			}
			<-joined
			want := TxIdle
			if explicit {
				want = TxAborted
			}
			if a.TransactionState() != want || a.transactions.writes != nil || a.transactions.catalog != nil || a.transactions.retainedMessage {
				t.Fatal("cancellation retained private effects")
			}
			transactionRows(t, b, "SELECT v FROM prefix")
			if explicit {
				controlsRequire(t, a, "ROLLBACK")
			}
			transactionEnd(t, b, holder, false)
			lockEmpty(t, e)
			transactionRows(t, a, "SELECT v FROM prefix")
			sessionSQL(t, a, "INSERT INTO prefix VALUES(9)")
			transactionRows(t, a, "SELECT v FROM prefix", 9)
		})
	}
}
func TestDriverBatchCanceledBetweenStatements(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			if explicit {
				transactionBegin(t, s)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := batchUnitStart(t, s, ctx)
			defer m.end(context.Canceled)
			if _, err := m.step(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
				t.Fatal(err)
			}
			cancel()
			_, err := m.step(statement(t, "INSERT INTO t VALUES(2)"))
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err = m.end(err); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			want := TxIdle
			if explicit {
				want = TxAborted
			}
			if s.TransactionState() != want || s.transactions.writes != nil {
				t.Fatal("between-statement error disposition")
			}
			if explicit {
				controlsRequire(t, s, "ROLLBACK")
			}
			transactionRows(t, s, "SELECT v FROM t")
		})
	}
}
