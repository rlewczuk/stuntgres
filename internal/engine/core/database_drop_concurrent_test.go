package core_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Public diagnostics and direct/fresh/SAME-held routes belong to the reference
// definition. This proof checks the real scope completion and ticket journals
// that public SQL cannot inspect, using genuine scope-owned database AX grants.
func TestDatabaseDropConcurrent(t *testing.T) {
	for _, missingOK := range []bool{false, true} {
		name := "plain"
		if missingOK {
			name = "if_exists"
		}
		t.Run(name, func(t *testing.T) {
			e, target, firstSession := databaseDropOwnerFixture(t)
			secondSession := enginetest.StartSession(t, e, nil)
			holderSession := enginetest.StartSession(t, e, nil)
			before := e.Locks.Inspect()
			ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
			defer cancel()
			holderOp, err := holderSession.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holderOp.Done()
			holder, err := e.StartStatement(holderOp)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Finish(errors.New("unfinished holder"))
			statement := &parser.DropDatabaseStmt{Name: target.DatabaseName}
			if err := holder.Gate(statement); err != nil {
				t.Fatal(err)
			}
			if err := holder.AcquireDatabase(target.Catalog.DatabaseAddress()); err != nil {
				t.Fatal(err)
			}
			resource := e.DatabaseResource(target.Catalog.DatabaseAddress())
			first := &databaseDropScopeWorker{session: firstSession, statement: statement, finished: make(chan struct{})}
			secondStatement := *statement
			secondStatement.IfExists = missingOK
			second := &databaseDropScopeWorker{session: secondSession, statement: &secondStatement, finished: make(chan struct{})}
			// Install cancellation, holder release and worker joins before launch.
			// Separate defers keep the second join reachable if the first
			// reports a fatal liveness failure.
			defer second.join(t)
			defer first.join(t)
			defer func() {
				cancel()
				holder.Finish(errors.New("release holder during cleanup"))
			}()
			first.start(ctx, e)
			enginetest.LockPending(t, e, 1)
			state := e.Locks.Inspect().Resources[resource]
			if state == nil || len(state.Holders) != 1 || state.Holders[holder.LockTicket].Mode != lock.AccessExclusive || len(state.Pending) != 1 || state.Pending[0].Ticket.Session() != firstSession.SessionToken || state.Pending[0].Mode != lock.AccessExclusive {
				t.Fatal("first issuer did not queue its genuine AX behind holder", state)
			}
			second.start(ctx, e)
			enginetest.LockPending(t, e, 2)
			state = e.Locks.Inspect().Resources[resource]
			if state == nil || len(state.Holders) != 1 || len(state.Pending) != 2 || state.Pending[0].Ticket.Session() != firstSession.SessionToken || state.Pending[1].Ticket.Session() != secondSession.SessionToken || state.Pending[1].Mode != lock.AccessExclusive {
				t.Fatal("second issuer did not queue behind first issuer on exact resource", state)
			}
			firstTicket, secondTicket := state.Pending[0].Ticket, state.Pending[1].Ticket
			holder.CompleteUtility()
			if err := holder.Finish(nil); err != nil {
				t.Fatal(err)
			}
			first.join(t)
			second.join(t)
			if first.err != nil || !first.completed || first.result.CommandTag != "DROP DATABASE" || first.result.Count != 0 || first.result.UtilityRows != 0 {
				t.Fatal("successful first removal lost utility completion/accounting", first.err, first.completed, first.result)
			}
			if missingOK {
				if second.err != nil || !second.completed || second.result.CommandTag != "DROP DATABASE" || second.result.Count != 0 || second.result.UtilityRows != 0 {
					t.Fatal("post-wait IF EXISTS lost utility completion/accounting", second.err, second.completed, second.result)
				}
			} else if second.err == nil || second.completed {
				t.Fatal("missing plain DROP was counted as a successful utility", second.err, second.completed)
			}
			after := e.Locks.Inspect(holder.LockTicket, firstTicket, secondTicket)
			if !maps.Equal(after.Owners, before.Owners) || !maps.Equal(after.Sessions, before.Sessions) || len(after.Journals[holder.LockTicket]) != 0 || len(after.Journals[firstTicket]) != 0 || len(after.Journals[secondTicket]) != 0 {
				t.Fatal("completed DROP pair leaked transaction tickets or their journals")
			}
			if state := after.Resources[resource]; state != nil && (len(state.Holders) != 0 || len(state.Pending) != 0) {
				t.Fatal("completed DROP pair retained its shared database grants/waiters", state)
			}
			if e.Cluster.Targets[target.DatabaseName] != nil || core.EngineDatabaseAdmissionCount(target) != 0 || len(target.Storage.Relations) != 0 {
				t.Fatal("successful removal retained live target authority/backing")
			}
		})
	}
}

type databaseDropScopeWorker struct {
	session         *session.Session
	statement       *parser.DropDatabaseStmt
	finished        chan struct{}
	started, joined bool
	completed       bool
	result          core.Result
	err             error
}

func (w *databaseDropScopeWorker) start(ctx context.Context, e *core.Engine) {
	w.started = true
	go func() {
		defer close(w.finished)
		op, err := w.session.Acquire(ctx)
		if err != nil {
			w.err = err
			return
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			w.err = err
			return
		}
		w.result, err = scope.Execute(w.statement, 0, nil)
		w.completed = scope.Completed
		w.err = scope.Finish(err)
	}()
}

func (w *databaseDropScopeWorker) join(t *testing.T) {
	t.Helper()
	if !w.started || w.joined {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
	defer cancel()
	select {
	case <-w.finished:
		w.joined = true
	case <-ctx.Done():
		t.Fatal("DROP scope failed to release terminal ownership", ctx.Err())
	}
}
