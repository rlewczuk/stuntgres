package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestStatementTimeoutAdmissionCopyReservation(t *testing.T) {
	for _, own := range []bool{false, true} {
		for _, before := range []bool{false, true} {
			t.Run(map[bool]string{false: "caller", true: "own"}[own]+"/"+map[bool]string{false: "waiting", true: "initial"}[before], func(t *testing.T) {
				e, sess, copyOp, scope := copyUnitStart(t, true)
				reader := &copyUnitReader{entered: make(chan struct{}), release: make(chan struct{})}
				copyDone := make(chan error, 1)
				var release sync.Once
				joined := false
				defer func() {
					release.Do(func() { close(reader.release) })
					if !joined {
						select {
						case <-copyDone:
						case <-time.After(5 * time.Second):
							t.Error("COPY did not join during teardown")
						}
					}
				}()
				syntax := copyUnitSyntax()
				go func() {
					_, err := scope.Copy(syntax, reader, nil)
					err = scope.Finish(err)
					copyOp.Done()
					copyDone <- err
				}()
				select {
				case <-reader.entered:
				case err := <-copyDone:
					joined = true
					t.Fatal("COPY ended before real reader barrier", err)
				case <-time.After(5 * time.Second):
					t.Fatal("COPY never entered reader")
				}
				owner := sess.Transactions
				owner.Admission.Lock()
				lease, done := owner.CopyLease, owner.CopyDone
				owner.Mu.Lock()
				ref, command, ticket := owner.Ref, owner.Command, owner.Ticket
				owner.Mu.Unlock()
				owner.Admission.Unlock()
				if lease == nil || done == nil {
					t.Fatal("COPY failed to retain logical reservation")
				}
				waiter, cancel := statementTimeoutAdmissionCommand(t, sess)
				cause := error(context.Canceled)
				if own {
					cause = session.StatementTimeoutCause
				}
				waiting := &copyWaitContext{Context: waiter.Context(), entered: make(chan struct{})}
				waiter.Ctx = waiting
				if before {
					cancel(cause)
				}
				finished := make(chan error, 1)
				waiterJoined := false
				defer func() {
					cancel(context.Canceled)
					if !waiterJoined {
						select {
						case <-finished:
						case <-time.After(5 * time.Second):
							t.Error("COPY waiter did not join during teardown")
						}
					}
				}()
				go func() {
					next, err := e.StartStatement(waiter)
					if next != nil {
						err = next.Finish(errors.New("waiter entered reserved COPY"))
					}
					finished <- err
				}()
				if !before {
					select {
					case <-waiting.entered:
					case err := <-finished:
						waiterJoined = true
						t.Fatal("waiter missed live COPY wait", err)
					case <-time.After(5 * time.Second):
						t.Fatal("waiter never observed COPY reservation")
					}
					cancel(cause)
				}
				select {
				case err := <-finished:
					waiterJoined = true
					if err != context.Canceled {
						t.Fatal("reserved waiter lost cancellation", err)
					}
					translated := waiter.CommandError(err)
					var diagnostic interface{ SQLState() string }
					if own {
						if !errors.As(translated, &diagnostic) || diagnostic.SQLState() != "57014" {
							t.Fatal("own COPY waiter lost timeout classification", translated)
						}
					} else if translated != context.Canceled {
						t.Fatal("ordinary COPY waiter became SQL timeout", translated)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("waiter waited for COPY reader instead of returning cancellation")
				}
				owner.Admission.Lock()
				owner.Mu.Lock()
				unchanged := owner.CopyLease == lease && owner.CopyDone == done && owner.Ref == ref && owner.Command == command && owner.Ticket == ticket && owner.State == session.TxActive
				owner.Mu.Unlock()
				owner.Admission.Unlock()
				if !unchanged || e.Locks.TransactionTicket(ref) != ticket {
					t.Fatal("canceled waiter disrupted reserved COPY owner")
				}
				release.Do(func() { close(reader.release) })
				select {
				case err := <-copyDone:
					joined = true
					if err != nil {
						t.Fatal("genuine COPY failed after waiter timeout", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("COPY did not finish after reader release")
				}
				owner.Admission.Lock()
				cleared := owner.CopyLease == nil && owner.CopyDone == nil
				owner.Admission.Unlock()
				if !cleared {
					t.Fatal("COPY reservation was not retired")
				}
				select {
				case <-done:
				default:
					t.Fatal("original reservation completion was not published")
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal("repeated COPY completion", err)
				}
				enginetest.TransactionRows(t, sess, "SELECT v FROM t", 1)
				if !enginetest.TransactionEnd(t, sess, ref, true).Committed {
					t.Fatal("COPY owner could not commit after canceled waiter")
				}
			})
		}
	}
}
