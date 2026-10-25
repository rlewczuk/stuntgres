package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Observe the particular owner resources a mistaken timeout disposal destroys.
func statementTimeoutAdmissionPreserved(t *testing.T, e *core.Engine, sess *session.Session) func() {
	t.Helper()
	o := sess.Transactions
	o.Mu.Lock()
	ref, command, ticket := o.Ref, o.Command, o.Ticket
	writes, deletes, roots, frontier := len(o.Writes), len(o.Deletes), len(o.Roots), o.Frontier
	o.Mu.Unlock()
	holds := e.Locks.Checkpoint(ticket)
	sess.Mu.Lock()
	budget := sess.Timeouts.Transaction.Generation
	timer, deadline, budgetRef := sess.Timeouts.Transaction.Timer, sess.Timeouts.Transaction.Deadline, sess.Timeouts.Transaction.Ref
	sess.Mu.Unlock()
	return func() {
		t.Helper()
		o.Mu.Lock()
		preserved := o.State == session.TxActive && o.Ref == ref && o.Command == command && o.Ticket == ticket && len(o.Writes) == writes && len(o.Deletes) == deletes && len(o.Roots) == roots && o.Frontier == frontier
		o.Mu.Unlock()
		if !preserved || e.Locks.TransactionTicket(ref) != ticket || !reflect.DeepEqual(e.Locks.Checkpoint(ticket), holds) {
			t.Fatal("canceled waiter changed active incarnation, private data, snapshot or live lock owner")
		}
		sess.Settings.Mu.Lock()
		application := sess.Settings.Value("application_name")
		sess.Settings.Mu.Unlock()
		if application != "timeout_admission_private" {
			t.Fatal("canceled waiter restored another operation's local settings", application)
		}
		sess.Mu.Lock()
		unchanged := sess.Timeouts.Transaction.Generation == budget && sess.Timeouts.Transaction.Timer == timer && sess.Timeouts.Transaction.Deadline == deadline && sess.Timeouts.Transaction.Ref == budgetRef
		sess.Mu.Unlock()
		if !unchanged {
			t.Fatal("canceled waiter changed the transaction budget generation")
		}
	}
}

func TestStatementTimeoutAdmissionOrdinaryCancellation(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(map[bool]string{false: "initial", true: "held_admission"}[queued]+"/"+map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
				e, sess, ref := statementTimeoutAdmissionOwner(t)
				preserved := statementTimeoutAdmissionPreserved(t, e, sess)
				op, cancel := statementTimeoutAdmissionCommand(t, sess)
				want := error(context.Canceled)
				cancelCaller := func() {
					if deadline {
						ctx, stop := context.WithDeadline(op.Context(), time.Unix(1, 0))
						t.Cleanup(stop)
						op.Ctx = ctx
						want = context.DeadlineExceeded
					} else {
						cancel(context.Canceled)
					}
				}
				var scope *core.StatementScope
				var err error
				if queued {
					scope, err = statementTimeoutAdmissionContended(t, e, op, cancel, cancelCaller)
				} else {
					cancelCaller()
					scope, err = e.StartStatement(op)
				}
				if scope != nil || err != want || op.CommandError(err) != want {
					t.Fatalf("ordinary cancellation scope=%p err=%v translated=%v; want exact %v", scope, err, op.CommandError(err), want)
				}
				preserved()
				transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 20)
				transactionEnd(t, sess, ref, false)
			})
		}
	}
	t.Run("precanceled_acquisition", func(t *testing.T) {
		e, sess, _ := statementTimeoutAdmissionOwner(t)
		preserved := statementTimeoutAdmissionPreserved(t, e, sess)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		op, _, err := sess.AcquireCommand(ctx)
		if op != nil || err != context.Canceled {
			t.Fatal("precanceled caller acquired command or lost identity", op, err)
		}
		preserved()
	})
}

func TestStatementTimeoutAdmissionReplacement(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "held_admission"}[queued], func(t *testing.T) {
			e := engine.New()
			sess := sessionStart(t, e, nil)
			sessionSQL(t, sess, "CREATE TABLE timeout_admission_rows(v INT PRIMARY KEY)")
			sessionSQL(t, sess, "SET transaction_timeout TO '1h'")
			old := transactionBegin(t, sess)
			op, cancel := statementTimeoutAdmissionCommand(t, sess)
			var fresh ids.TxRef
			var preserved func()
			replace := func(admitted bool) {
				control := transactionOperation(t, sess)
				if admitted {
					if _, err := core.EngineEndAdmitted(e, control, sess.Transactions, old, false, false, false, time.Time{}, nil); err != nil {
						t.Fatal(err)
					}
					var err error
					fresh, err = core.EngineBeginAdmitted(e, control, sess.Transactions, time.Now())
					if err != nil {
						t.Fatal(err)
					}
				} else {
					transactionEnd(t, sess, old, false)
					fresh = transactionBegin(t, sess)
				}
				control.Done()
				// The waiter is stopped at its context barrier, so release only
				// physical admission for lawful replacement-owner setup, then
				// reacquire before allowing the observed waiter to continue.
				if admitted {
					sess.Transactions.Admission.Unlock()
				}
				func() {
					if admitted {
						defer sess.Transactions.Admission.Lock()
					}
					sessionSQL(t, sess, "INSERT INTO timeout_admission_rows VALUES(20)")
					sessionSQL(t, sess, "SET LOCAL application_name TO timeout_admission_private")
				}()
				preserved = statementTimeoutAdmissionPreserved(t, e, sess)
				cancel(session.StatementTimeoutCause)
			}
			var scope *core.StatementScope
			var err error
			if queued {
				scope, err = statementTimeoutAdmissionContended(t, e, op, cancel, func() { replace(true) })
			} else {
				replace(false)
				preserved = statementTimeoutAdmissionPreserved(t, e, sess)
				scope, err = e.StartStatement(op)
			}
			if scope != nil || err != context.Canceled || fresh == old || op.OriginTransaction != old {
				t.Fatal("stale command lost original incarnation or timeout", scope, err, old, fresh, op.OriginTransaction)
			}
			if preserved != nil {
				preserved()
			}
			if sess.TransactionState() != session.TxActive || sess.Transactions.Ref != fresh || sess.Transactions.Ticket == nil {
				t.Fatal("old timeout aborted replacement owner")
			}
			transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 20)
			if !transactionEnd(t, sess, fresh, true).Committed {
				t.Fatal("replacement could not commit")
			}
			transactionRows(t, sess, "SELECT v FROM timeout_admission_rows", 20)
		})
	}
}

func TestStatementTimeoutAdmissionIdleOrigin(t *testing.T) {
	e := engine.New()
	sess := sessionStart(t, e, nil)
	op, cancel := statementTimeoutAdmissionCommand(t, sess)
	if op.OriginTransaction != (ids.TxRef{}) {
		t.Fatal("idle acquisition captured an incarnation")
	}
	ref := transactionBegin(t, sess)
	cancel(session.StatementTimeoutCause)
	scope, err := e.StartStatement(op)
	if scope != nil || err != context.Canceled || sess.TransactionState() != session.TxActive || sess.Transactions.Ref != ref || op.OriginTransaction != (ids.TxRef{}) {
		t.Fatal("idle-origin timeout poisoned later explicit owner", scope, err)
	}
	transactionEnd(t, sess, ref, false)
}

func TestStatementTimeoutAdmissionAuthority(t *testing.T) {
	for _, kind := range []string{"foreign", "copied", "forged_self", "forged_member", "nonmember", "released", "ending"} {
		t.Run(kind, func(t *testing.T) {
			e, sess, _ := statementTimeoutAdmissionOwner(t)
			op, cancel := statementTimeoutAdmissionCommand(t, sess)
			cancel(session.StatementTimeoutCause)
			target := e
			switch kind {
			case "foreign":
				target = engine.New()
			case "copied":
				copied := *op
				op = &copied
			case "forged_member":
				copied := *op
				copied.Self = &copied
				op = &copied
			case "forged_self":
				op.Self = nil
			case "nonmember":
				sess.Mu.Lock()
				delete(sess.Operations, op)
				sess.Mu.Unlock()
			case "released":
				op.Done()
			case "ending":
				sess.RequestEnd(session.Terminated)
			}
			// Restore malformed membership before registered Done cleanup.
			if kind == "forged_self" {
				defer func() { op.Self = op }()
			}
			if kind == "nonmember" {
				defer func() { sess.Mu.Lock(); sess.Operations[op] = struct{}{}; sess.Mu.Unlock() }()
			}
			want := session.TransactionAuthority(core.DomainOf(target), op)
			scope, err := target.StartStatement(op)
			if scope != nil || err == nil || want == nil || err.Error() != want.Error() || errors.Is(err, context.Canceled) {
				t.Fatal("invalid authority lost precedence to own cause", scope, err, want)
			}
			if kind != "ending" && sess.TransactionState() != session.TxActive {
				t.Fatal("invalid authority changed genuine owner")
			}
		})
	}
	t.Run("retired_across_admission", func(t *testing.T) {
		e, sess, _ := statementTimeoutAdmissionOwner(t)
		op, cancel := statementTimeoutAdmissionCommand(t, sess)
		scope, err := statementTimeoutAdmissionContended(t, e, op, cancel, func() { cancel(session.StatementTimeoutCause); op.Done() })
		if scope != nil || err == nil || err.Error() != "stuntgres: inactive operation" || sess.TransactionState() != session.TxActive {
			t.Fatal("retired waiter mutated owner or lost authority precedence", scope, err)
		}
	})
	t.Run("fatal_precedence", func(t *testing.T) {
		e, sess, _ := statementTimeoutAdmissionOwner(t)
		op, _ := statementTimeoutAdmissionCommand(t, sess)
		// Deliver an actually due canonical FATAL budget before installing the
		// test-owned cause; a pending statement ERROR otherwise has priority.
		sess.Mu.Lock()
		slot := &sess.Timeouts.Transaction
		if slot.Timer == nil {
			sess.Mu.Unlock()
			t.Fatal("no real transaction budget to expire")
		}
		slot.Timer.Stop()
		slot.Deadline = time.Now().Add(-time.Second)
		sess.ExpireTimeoutLocked(slot, slot.Generation, sess.Token(), "25P04", time.Now())
		fatal := sess.Timeouts.Fatal
		sess.Mu.Unlock()
		if fatal == nil {
			t.Fatal("canonical expiry failed to terminate session")
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(context.Canceled)
		op.Ctx = ctx
		cancel(session.StatementTimeoutCause)
		scope, err := e.StartStatement(op)
		var diagnostic interface{ SQLState() string }
		if scope != nil || !errors.As(err, &diagnostic) || diagnostic.SQLState() != "25P04" || err.Error() != fatal.Error() {
			t.Fatal("own cause hid canonical FATAL", scope, err)
		}
	})
}

func TestStatementTimeoutAdmissionIdleNoBirth(t *testing.T) {
	e := engine.New()
	sess := sessionStart(t, e, nil)
	op, cancel := statementTimeoutAdmissionCommand(t, sess)
	cancel(session.StatementTimeoutCause)
	scope, err := e.StartStatement(op)
	if scope != nil || err != context.Canceled || sess.TransactionState() != session.TxIdle || sess.Transactions.Ref != (ids.TxRef{}) || sess.Transactions.Ticket != nil {
		t.Fatal("idle own timeout created a transaction or lock owner", scope, err)
	}
	ref := transactionBegin(t, sess)
	transactionEnd(t, sess, ref, false)
}

func TestStatementTimeoutAdmissionNonCommand(t *testing.T) {
	e, sess, _ := statementTimeoutAdmissionOwner(t)
	preserved := statementTimeoutAdmissionPreserved(t, e, sess)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	op, err := sess.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	cancel(session.StatementTimeoutCause)
	scope, err := e.StartStatement(op)
	if scope != nil || err != context.Canceled {
		t.Fatal("non-command cancellation lost context identity", scope, err)
	}
	preserved()
}

func TestStatementTimeoutAdmissionPreexpiredAcquisition(t *testing.T) {
	e, sess, _ := statementTimeoutAdmissionOwner(t)
	preserved := statementTimeoutAdmissionPreserved(t, e, sess)
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	op, _, err := sess.AcquireCommand(ctx)
	if op != nil || err != context.DeadlineExceeded {
		t.Fatal("preexpired caller acquired command or lost deadline identity", op, err)
	}
	preserved()
}
