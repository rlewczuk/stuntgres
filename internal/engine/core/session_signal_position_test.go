package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestSessionSignalRelationPosition(t *testing.T) {
	for _, tc := range []struct {
		name, query, position string
		cached                bool
	}{
		{"analysis", "INSERT INTO signal_position VALUES(9)", "13", false},
		{"cached_execution", "INSERT INTO signal_position VALUES(9)", "", true},
		{"copy_execution", "COPY signal_position FROM STDIN", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			victim := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, owner, "CREATE TABLE signal_position(v integer)")
			statement := enginetest.Statement(t, tc.query)
			var expected *core.Description
			if tc.cached {
				op, _, err := victim.AcquireCommand(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				description, err := e.Describe(op, statement)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &description
			}
			transaction := enginetest.TransactionBegin(t, owner)
			t.Cleanup(func() { enginetest.TransactionEnd(t, owner, transaction, false) })
			enginetest.SessionSQL(t, owner, "LOCK TABLE signal_position IN ACCESS EXCLUSIVE MODE")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "signal_position")
			resource := lock.Resource{ObjectRef: ref}
			owner.Transactions.Mu.Lock()
			ownerTicket := owner.Transactions.Ticket
			owner.Transactions.Mu.Unlock()

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			op, _, err := victim.AcquireCommand(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			scope.Expected = expected
			done := make(chan error, 1)
			joined := false
			t.Cleanup(func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Error("positioned relation wait did not join")
					}
				}
			})
			go func() {
				// StartStatement owns admission, but Gate establishes the actual
				// transaction incarnation and lock ticket before relation lookup.
				err := scope.Gate(statement)
				if err == nil {
					err = scope.AcquireRelationTarget(statement, statement, true, false)
				}
				err = op.CommandError(scope.Finish(err))
				op.Done()
				done <- err
			}()
			for {
				select {
				case err := <-done:
					joined = true
					t.Fatalf("relation admission completed before the required wait: %v", err)
				case <-ctx.Done():
					t.Fatal("relation request never reached its owner-backed queue", ctx.Err())
				default:
				}
				victim.Transactions.Mu.Lock()
				victimTicket := victim.Transactions.Ticket
				victim.Transactions.Mu.Unlock()
				state := e.Locks.Inspect()
				entry := state.Resources[resource]
				if entry != nil && victimTicket != nil && len(entry.Pending) == 1 {
					waiter := entry.Pending[0]
					if waiter.Ticket == victimTicket && waiter.Mode == lock.RowExclusive && !waiter.Terminal && entry.Holders[ownerTicket].Mode == lock.AccessExclusive {
						break
					}
				}
				runtime.Gosched()
			}
			victim.Mu.Lock()
			victim.TerminateLocked()
			canonical := victim.Timeouts.Fatal
			before := *canonical
			victim.Mu.Unlock()
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("terminated relation wait did not join", ctx.Err())
			}
			want := sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command", Position: tc.position}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatalf("relation fatal %#v (%v), want %#v", diagnostic, err, want)
			}
			victim.Mu.Lock()
			unchanged := victim.Timeouts.Fatal == canonical && *canonical == before && canonical.Position == ""
			victim.Mu.Unlock()
			if !unchanged || diagnostic == canonical {
				t.Fatal("source annotation mutated or borrowed canonical physical fatal")
			}
			if err := victim.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			physicalWant := want
			physicalWant.Position = ""
			var physical *sqlstate.Error
			if err := victim.Check(context.Background()); !errors.As(err, &physical) || *physical != physicalWant || physical == canonical {
				t.Fatal("later physical API inherited command position or borrowed fatal", err)
			}
			if victim.Reason != session.Terminated {
				t.Fatal("termination lifecycle reason changed", victim.Reason)
			}
		})
	}
}
