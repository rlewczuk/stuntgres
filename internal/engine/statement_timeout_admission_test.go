package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// atCheckpoint runs with physical admission held and the initial Err sampled.
func statementTimeoutAdmissionContended(t *testing.T, e *Engine, op *session.Operation, cancel context.CancelCauseFunc, atCheckpoint func()) (*StatementScope, error) {
	t.Helper()
	checkpoint := &statementTimeoutAdmissionContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	op.Ctx = checkpoint
	owner := op.Session.Transactions
	owner.Admission.Lock()
	held := true
	var release sync.Once
	unblock := func() {
		release.Do(func() { close(checkpoint.release) })
		if held {
			owner.Admission.Unlock()
			held = false
		}
	}
	type outcome struct {
		scope *StatementScope
		err   error
	}
	finished := make(chan outcome, 1)
	joined := false
	defer func() {
		cancel(context.Canceled)
		unblock()
		if !joined {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("admission worker did not join during teardown")
			}
		}
	}()
	go func() {
		scope, err := e.StartStatement(op)
		if scope != nil {
			_ = scope.Finish(errors.New("unexpected canceled admission"))
		}
		finished <- outcome{scope, err}
	}()
	select {
	case <-checkpoint.entered:
	case result := <-finished:
		joined = true
		t.Fatalf("command returned before admission checkpoint: scope=%p err=%v", result.scope, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("command did not reach admission checkpoint")
	}
	atCheckpoint()
	unblock()
	select {
	case result := <-finished:
		joined = true
		return result.scope, result.err
	case <-time.After(5 * time.Second):
		t.Fatal("canceled command did not finish after admission release")
		return nil, nil
	}
}

type statementTimeoutAdmissionContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *statementTimeoutAdmissionContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return err
}

func statementTimeoutAdmissionOwner(t *testing.T, isolation ...session.Isolation) (*Engine, *session.Session, ids.TxRef) {
	t.Helper()
	e := New()
	sess := sessionStart(t, e, nil)
	// sessionSQL invokes package owner APIs, not database/sql or public adapters.
	sessionSQL(t, sess, "CREATE TABLE timeout_admission_rows(v INT PRIMARY KEY)")
	sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(10)")
	// This independent transaction budget is a resource-retirement witness,
	// not the statement timer or an enlarged execution-timeout fixture.
	sessionSQL(t, sess, "SET transaction_timeout TO '1h'")
	op := transactionOperation(t, sess)
	level := session.RepeatableRead
	if len(isolation) != 0 {
		level = isolation[0]
	}
	ref, err := e.Begin(op, session.TxOptions{Isolation: level})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sess.IsLive() && sess.TransactionState() != session.TxIdle {
			transactionEnd(t, sess, ref, false)
		}
	})
	sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(20)")
	sessionSQL(t, sess, "DELETE FROM timeout_admission_rows WHERE v=10")
	sessionSQL(t, sess, "SET LOCAL application_name TO timeout_admission_private")
	owner := sess.Transactions
	owner.Mu.Lock()
	staged := len(owner.Writes) != 0 && len(owner.Deletes) != 0 && owner.SnapshotSet && len(owner.Roots) != 0 && owner.Ticket != nil
	owner.Mu.Unlock()
	if !staged {
		t.Fatal("setup did not establish real private mutations, snapshot and lock owner")
	}
	return e, sess, ref
}

func statementTimeoutAdmissionCommand(t *testing.T, sess *session.Session) (*session.Operation, context.CancelCauseFunc) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(context.Canceled) })
	op, _, err := sess.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	if !op.CommandAdmission {
		t.Fatal("AcquireCommand did not publish a command operation")
	}
	return op, cancel
}

func statementTimeoutAdmissionOutcome(t *testing.T, e *Engine, sess *session.Session, ref ids.TxRef, op *session.Operation, scope *StatementScope, err error) {
	t.Helper()
	if scope != nil {
		_ = scope.Finish(errors.New("unexpected timeout admission"))
		t.Fatal("own timeout returned a statement scope")
	}
	if !errors.Is(err, context.Canceled) || context.Cause(op.Context()) != session.StatementTimeoutCause {
		t.Fatalf("pre-scope error=%v cause=%v; want canceled with exact own cause", err, context.Cause(op.Context()))
	}
	var diagnostic *sqlstate.Error
	if translated := op.CommandError(err); !errors.As(translated, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
		t.Fatalf("command error=%#v; want own statement timeout 57014", translated)
	}
	owner := sess.Transactions
	owner.Mu.Lock()
	state, current := owner.State, owner.Ref
	clean := owner.Writes == nil && owner.Deletes == nil && owner.Roots == nil && owner.SequenceCatalog == nil && owner.Frontier == 0 && owner.Catalog == nil && owner.NotificationActions == nil && owner.NotificationEvents == nil && owner.Ticket == nil
	characteristics := owner.Isolation == owner.EntryIsolation && owner.ReadOnly == owner.EntryReadOnly && owner.Deferrable == owner.EntryDeferrable
	owner.Mu.Unlock()
	if state != session.TxAborted || current != ref {
		t.Fatalf("own pre-scope timeout returned with state=%v ref=%v; want immediate TxAborted=%v and originating ref=%v", state, current, session.TxAborted, ref)
	}
	if !clean {
		t.Fatal("own pre-scope timeout retained private mutations, snapshot, journal, notifications or lock ticket")
	}
	if !characteristics {
		t.Fatal("failed block did not restore entry transaction characteristics")
	}
	sess.Mu.Lock()
	budgetStopped := sess.Timeouts.Transaction.Timer == nil && sess.Timeouts.Transaction.Deadline.IsZero()
	sess.Mu.Unlock()
	if !budgetStopped {
		t.Fatal("pre-scope ERROR retained the transaction budget")
	}
	sess.Settings.Mu.Lock()
	application := sess.Settings.Value("application_name")
	sess.Settings.Mu.Unlock()
	if application != "" {
		t.Fatalf("failed transaction retained local application_name=%q", application)
	}
	// A fresh real scope must reject work immediately, without aborted-state polling.
	next := transactionOperation(t, sess)
	follow, followErr := e.StartStatement(next)
	if followErr != nil {
		next.Done()
		t.Fatal(followErr)
	}
	_, followErr = follow.Execute(statement(t, "SELECT v FROM timeout_admission_rows"), 0, nil)
	followErr = follow.Finish(followErr)
	next.Done()
	diagnostic = nil
	if !errors.As(followErr, &diagnostic) || diagnostic.Code != "25P02" {
		t.Fatalf("immediate follow-on owner work=%v; want 25P02", followErr)
	}
	transactionEnd(t, sess, ref, false)
	if sess.TransactionState() != session.TxIdle {
		t.Fatal("rollback did not recover idle owner")
	}
	transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 10)
	fresh := transactionBegin(t, sess)
	sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(30)")
	if !transactionEnd(t, sess, fresh, true).Committed {
		t.Fatal("recovered owner could not commit healthy work")
	}
	transactionRows(t, sess, "SELECT v FROM timeout_admission_rows ORDER BY v", 10, 30)
}
