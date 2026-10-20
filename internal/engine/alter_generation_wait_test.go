package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestAlterGeneratedExpressionAuthorizationAfterWait(t *testing.T) {
	e, admin, actor, ref := generatedAuthorizationFixture(t)
	old, store := e.catalog.state.Get(ref.Address), e.storage.Relations[ref.Address]
	blocker, err := e.StartStatement(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Finish(errors.New("release admission blocker"))
	if err := blocker.gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	if err := e.locks.Acquire(context.Background(), blocker.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	done := make(chan error, 1)
	finished := make(chan struct{})
	expr := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "/"}}}, Args: []parser.Expr{alterGenerationLiteral(1), alterGenerationLiteral(0)}}
	go func() {
		defer close(finished)
		_, err := e.Execute(op, alterGenerationStatement(alterGenerationAction(expr)), 0, nil)
		done <- err
	}()
	defer func() {
		cancel()
		blocker.Finish(errors.New("release admission blocker"))
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("admission worker did not join")
		}
	}()
	lockPending(t, e, 1)
	// Change the real session principal while AX admission is parked, rather than
	// changing the ALTER AST or replacing its authorization result with a mock.
	op.Session.mu.Lock()
	op.Session.authorization.state.CurrentUser = actor.RoleState().CurrentUser
	op.Session.authorization.revision++
	op.Session.mu.Unlock()
	if err := blocker.Finish(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "must be owner of table g" {
			t.Fatal("ownership not recaptured after AX", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admission worker remained parked")
	}
	if e.catalog.state.Get(ref.Address) != old || e.storage.Relations[ref.Address] != store {
		t.Fatal("denied waiter published generation")
	}
	lockEmpty(t, e)
}
