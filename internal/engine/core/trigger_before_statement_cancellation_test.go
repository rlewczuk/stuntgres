package core_test

import (
	"bytes"
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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

type beforeStatementCancelOutcome struct {
	scope *core.StatementScope
	err   error
}

func beforeStatementCancelWorker(e *core.Engine, owner *session.Session, ctx context.Context, done chan<- beforeStatementCancelOutcome) {
	op, err := owner.Acquire(ctx)
	if err != nil {
		done <- beforeStatementCancelOutcome{err: err}
		return
	}
	var out beforeStatementCancelOutcome
	defer func() { op.Done(); done <- out }()
	scope, err := e.StartStatement(op)
	if err != nil {
		out.err = err
		return
	}
	out.scope = scope
	statements, err := parser.Parse("INSERT INTO bst_cancel VALUES(1)")
	if err != nil {
		out.err = scope.Finish(err)
		return
	}
	result, err := scope.Execute(statements[0], 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	out.err = scope.Finish(err)
}

func TestTriggerBeforeStatementCancellation(t *testing.T) {
	t.Run("blocked_helper", func(t *testing.T) {
		e := engine.New()
		owner := enginetest.StartSession(t, e, nil)
		holder := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			"CREATE TABLE bst_cancel(id integer)", "CREATE TABLE bst_cancel_audit(id integer)", "CREATE TABLE bst_cancel_helper(id integer)",
			"CREATE FUNCTION bst_cancel_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_cancel_audit VALUES(1); INSERT INTO bst_cancel_helper VALUES(1); RETURN NULL; END$$",
			"CREATE TRIGGER s BEFORE INSERT ON bst_cancel EXECUTE FUNCTION bst_cancel_body()",
		} {
			enginetest.SessionSQL(t, owner, q)
		}
		enginetest.SessionSQL(t, holder, "BEGIN")
		defer enginetest.SessionSQL(t, holder, "ROLLBACK")
		enginetest.SessionSQL(t, holder, "LOCK TABLE bst_cancel_helper IN ACCESS EXCLUSIVE MODE")
		ref := enginetest.RoleTableRef(t, e, "bst_cancel_helper")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan beforeStatementCancelOutcome, 1)
		go beforeStatementCancelWorker(e, owner, ctx, done)
		joined := false
		defer func() {
			cancel()
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("cancel worker did not join")
				}
			}
		}()
		deadline := time.Now().Add(5 * time.Second)
		stacks := make([]byte, 64<<10)
		for {
			select {
			case out := <-done:
				joined = true
				t.Fatal("worker ended before real helper admission", out.err)
			default:
			}
			state := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]
			pending := false
			if state != nil && len(state.Holders) != 0 {
				for _, waiter := range state.Pending {
					if waiter.Mode == lock.RowExclusive && !waiter.Terminal {
						pending = true
					}
				}
			}
			n := runtime.Stack(stacks, true)
			if n == len(stacks) {
				stacks = make([]byte, len(stacks)*2)
				continue
			}
			parked := false
			for _, stack := range bytes.Split(stacks[:n], []byte("\n\n")) {
				if bytes.Contains(stack, []byte("(*relationRequest).wait")) && bytes.Contains(stack, []byte("(*statementHost).BeforeStatement")) && bytes.Contains(stack, []byte("beforeStatementCancelWorker")) {
					parked = true
					break
				}
			}
			if pending && parked {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("real BEFORE helper worker never parked on its admitted relation")
			}
			runtime.Gosched()
		}
		cancel()
		var out beforeStatementCancelOutcome
		select {
		case out = <-done:
			joined = true
		case <-time.After(5 * time.Second):
			t.Fatal("cancel worker did not terminate")
		}
		if !errors.Is(out.err, context.Canceled) || out.scope == nil {
			t.Fatal("cancellation did not reject real invocation", out.err)
		}
		if len(core.ScopeAfterTestFrames(out.scope)) != 0 || core.ScopeActiveSQLRoutineDepth(out.scope) != 0 || out.scope.Op.EffectiveUser != nil {
			t.Fatal("canceled owner retained frame or routine authority")
		}
		for _, name := range []string{"bst_cancel", "bst_cancel_audit", "bst_cancel_helper"} {
			ref := enginetest.RoleTableRef(t, e, name)
			if len(e.Storage.Relations[ref.Address].Versions) != 0 {
				t.Fatal("canceled invocation published", name)
			}
		}
		enginetest.SessionSQL(t, holder, "ROLLBACK")
		enginetest.SessionSQL(t, owner, "INSERT INTO bst_cancel VALUES(2)")
		for _, name := range []string{"bst_cancel", "bst_cancel_audit", "bst_cancel_helper"} {
			ref := enginetest.RoleTableRef(t, e, name)
			if len(e.Storage.Relations[ref.Address].Versions) != 1 {
				t.Fatal("following command skipped or replayed body", name)
			}
		}
	})
	t.Run("after_completed_body", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e, s, read, relation, d := triggerUnitFixtureContext(t, ctx, "BEGIN CREATE TABLE bst_cancel_private(id integer); RETURN NULL; END")
		p := triggerUnitDefinition(t, read, relation, d, "s", stored.TriggerInsert).Parts()
		p.Level = stored.TriggerStatement
		definition, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		relation = triggerUnitInstall(t, e, relation, definition)
		triggerUnitAdmit(t, s, relation)
		ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
		if err := s.WriteHost().BeforeStatement(ref, stored.TriggerInsert, nil); err != nil {
			t.Fatal(err)
		}
		if s.Catalog == nil || len(core.ScopeBeforeTestMap(s)) != 1 {
			t.Fatal("body did not complete in its private journal")
		}
		published := e.Catalog.State
		cancel()
		if err := s.Finish(nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if e.Catalog.State != published || len(core.ScopeAfterTestFrames(s)) != 0 || core.ScopeActiveSQLRoutineDepth(s) != 0 {
			t.Fatal("post-body cancellation published or retained invocation")
		}
		if err := s.WriteHost().BeforeStatement(ref, stored.TriggerInsert, nil); err == nil {
			t.Fatal("finished owner replayed body")
		}
	})
}
