package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestStatementTimeoutAdmissionFullCleanup(t *testing.T) {
	e, sess, ref := statementTimeoutAdmissionOwner(t, session.Serializable)
	// Permanent CREATE is deliberately unsupported in explicit blocks. TEMP
	// creation uses the real validated owner catalog/storage overlay instead.
	private := sessionSQL(t, sess, "CREATE TEMP TABLE timeout_admission_private(v INT)").created
	sess.Transactions.Mu.Lock()
	journal := sess.Transactions.Catalog
	staged := journal != nil && journal.Descriptors[private.Address] != nil && journal.Storage[private.Address].Replacement != nil
	sess.Transactions.Mu.Unlock()
	if !staged {
		t.Fatal("TEMP creation did not stage a real private descriptor and store")
	}
	if _, published := e.CatalogRead().Descriptor(private.Address); published {
		t.Fatal("private TEMP descriptor was published before transaction end")
	}
	sessionSQL(t, sess, "NOTIFY timeout_admission_channel, 'private'")
	sessionSQL(t, sess, "DECLARE timeout_admission_cursor NO SCROLL CURSOR WITHOUT HOLD FOR SELECT v FROM timeout_admission_rows")
	cursor := payloadOf(sess).cursors.entries["timeout_admission_cursor"]
	pin := cursor.source.View
	if pin.Load() == nil {
		t.Fatal("cursor did not retain real view authority")
	}
	e.mu.Lock()
	node := e.visibility.RelevantSerializableLocked(ref)
	e.mu.Unlock()
	if node == nil {
		t.Fatal("serializable setup did not enroll snapshot owner")
	}
	ticket := sess.Transactions.Ticket
	if len(e.locks.Inspect(ticket).Journals[ticket]) == 0 {
		t.Fatal("setup has no genuine lock contributions")
	}
	// Enlist the existing unit provider through the actual participant registry.
	// This is callback lifecycle coverage, not a remote/network I/O claim.
	work := transactionOperation(t, sess)
	scope, err := e.StartStatement(work)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "SELECT v FROM timeout_admission_rows"), 0, nil)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if err := result.Rows.Close(); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	provider := &fdwUnitLifecycle{tx: &fdwUnitTransaction{}}
	provider.tx.check = func() {
		if !sess.Transactions.Mu.TryLock() {
			t.Error("foreign cleanup holds owner mutex")
		} else {
			sess.Transactions.Mu.Unlock()
		}
		if !e.mu.TryLock() {
			t.Error("foreign cleanup holds engine mutex")
		} else {
			e.mu.Unlock()
		}
		if !sess.Mu.TryLock() {
			t.Error("foreign cleanup holds session mutex")
		} else {
			sess.Mu.Unlock()
		}
	}
	if _, err := scope.enlistForeign(work.Context(), provider, catalog.ForeignRequest{HandlerOID: 1, ServerOID: 2, MappingOID: 3, RoleOID: 4}, true); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	work.Done()
	op, cancel := statementTimeoutAdmissionCommand(t, sess)
	cancel(session.StatementTimeoutCause)
	failed, err := e.StartStatement(op)
	if failed != nil || err != context.Canceled || sess.TransactionState() != session.TxAborted {
		t.Fatal("full cleanup did not synchronously fail block", failed, err)
	}
	if pin.Load() != nil || payloadOf(sess).cursors.entries["timeout_admission_cursor"] != nil {
		t.Fatal("pre-scope failure retained cursor producer/view")
	}
	if !reflect.DeepEqual(provider.tx.calls, []string{"begin-statement", "statement-success", "rollback"}) {
		t.Fatal("foreign participant not retired exactly once", provider.tx.calls)
	}
	e.mu.Lock()
	relevant := e.visibility.RelevantSerializableLocked(ref)
	e.mu.Unlock()
	if relevant != nil {
		t.Fatal("pre-scope failure retained serializable ownership")
	}
	if e.locks.TransactionTicket(ref) != nil {
		t.Fatal("retired ticket remains registered")
	}
	// Another lawful owner can actually acquire the formerly conflicting hold.
	peer := sessionStart(t, e, nil)
	peerRef := transactionBegin(t, peer)
	peerTicket := peer.Transactions.Ticket
	rowRef := catalogRef(t, e, e.CatalogRead(), "timeout_admission_rows")
	if _, err := e.locks.StartAcquire(context.Background(), peerTicket, lock.Resource{ObjectRef: rowRef}, lock.AccessExclusive, lock.Policy{NoWait: true}); err != nil {
		t.Fatal("failed owner retained conflicting table hold", err)
	}
	transactionEnd(t, peer, peerRef, false)
	// Shared outcome asserts all private roots/journals/events/settings cleared,
	// immediate 25P02, rollback and unchanged committed rows, then healthy commit.
	statementTimeoutAdmissionOutcome(t, e, sess, ref, op, failed, err)
	if _, published := e.CatalogRead().Descriptor(private.Address); published {
		t.Fatal("failed private TEMP catalog change was published")
	}
	if !reflect.DeepEqual(provider.tx.calls, []string{"begin-statement", "statement-success", "rollback"}) {
		t.Fatal("rollback repeated foreign participant disposal", provider.tx.calls)
	}
}

func TestStatementTimeoutAdmissionSavepoint(t *testing.T) {
	e, sess, ref := statementTimeoutAdmissionOwner(t)
	parentTicket := sess.Transactions.Ticket
	controlsRequire(t, sess, "SAVEPOINT timeout_mark")
	mark := sess.Transactions.Savepoints[len(sess.Transactions.Savepoints)-1]
	sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(30)")
	sessionSQL(t, sess, "SET LOCAL application_name TO timeout_admission_child")
	sessionSQL(t, sess, "LOCK TABLE timeout_admission_rows IN ACCESS EXCLUSIVE MODE")
	for range 2 {
		op, cancel := statementTimeoutAdmissionCommand(t, sess)
		cancel(session.StatementTimeoutCause)
		scope, err := e.StartStatement(op)
		if scope != nil || err != context.Canceled || sess.TransactionState() != session.TxAborted || sess.Transactions.Ref != ref || sess.Transactions.Ticket != parentTicket {
			t.Fatal("savepoint timeout lost failed block or parent lock owner", scope, err)
		}
		if !reflect.DeepEqual(e.locks.Checkpoint(parentTicket), mark.Locks) {
			t.Fatal("savepoint timeout did not restore parent lock journal")
		}
		sess.Settings.Mu.Lock()
		application := sess.Settings.Value("application_name")
		sess.Settings.Mu.Unlock()
		if application != "timeout_admission_private" {
			t.Fatal("savepoint timeout failed to restore parent local setting", application)
		}
		sess.Mu.Lock()
		stopped := sess.Timeouts.Transaction.Timer == nil && sess.Timeouts.Transaction.Deadline.IsZero()
		sess.Mu.Unlock()
		if !stopped {
			t.Fatal("failed savepoint retained/rearmed transaction budget")
		}
		op.Done()
	}
	controlsRequire(t, sess, "ROLLBACK TO timeout_mark")
	transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 20)
	sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(40)")
	if !transactionEnd(t, sess, ref, true).Committed {
		t.Fatal("recovered savepoint could not commit parent work")
	}
	transactionRows(t, sess, "SELECT v FROM timeout_admission_rows ORDER BY v", 20, 40)
}

func TestStatementTimeoutAdmissionAlreadyAborted(t *testing.T) {
	e, sess, ref := statementTimeoutAdmissionOwner(t)
	for range 2 {
		op, cancel := statementTimeoutAdmissionCommand(t, sess)
		cancel(session.StatementTimeoutCause)
		scope, err := e.StartStatement(op)
		if scope != nil || err != context.Canceled || sess.TransactionState() != session.TxAborted || sess.Transactions.Ref != ref || sess.Transactions.Ticket != nil || sess.Transactions.Writes != nil {
			t.Fatal("repeated own failure changed failed-block disposal", scope, err)
		}
		op.Done()
	}
	transactionEnd(t, sess, ref, false)
	transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 10)
}

func TestStatementTimeoutAdmissionProtocol(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		e, sess, ref := statementTimeoutAdmissionOwner(t)
		cycle := sess.NewProtocolCycle()
		op, cancel := statementTimeoutAdmissionCommand(t, sess)
		cancel(session.StatementTimeoutCause)
		scope, err := e.StartProtocolCommand(op, cycle)
		statementTimeoutAdmissionOutcome(t, e, sess, ref, op, scope, err)
	})
	t.Run("foreign_cycle", func(t *testing.T) {
		e, sess, _ := statementTimeoutAdmissionOwner(t)
		peer := sessionStart(t, e, nil)
		op, cancel := statementTimeoutAdmissionCommand(t, sess)
		cancel(session.StatementTimeoutCause)
		scope, err := e.StartProtocolCommand(op, peer.NewProtocolCycle())
		if scope != nil || err == nil || err.Error() != "stuntgres: foreign protocol cycle" || errors.Is(err, context.Canceled) || sess.TransactionState() != session.TxActive {
			t.Fatal("foreign cycle lost precedence or changed genuine block", scope, err)
		}
	})
}
