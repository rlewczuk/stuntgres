package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type originDropWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

// Installed only after ordinary statement admission. The actual drop select
// borrows Done after releasing Engine.Mu, providing a deterministic wait gate.
func (c *originDropWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func originSessionNativeCall(t *testing.T, e *core.Engine, s *session.Session, kind functions.ReplicationOriginKind) sqltype.Value {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	value, err := originStateUnitCall(t, e, core.HostOf(scope), kind, "", 0, false)
	if finishErr := scope.Finish(err); err == nil {
		err = finishErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReplicationOriginSessionDropWait(t *testing.T) {
	for _, outcome := range []string{"reset", "physical_end", "cancel", "replacement"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			e := engine.NewWithSettings(nil, "", true)
			owner := enginetest.StartSession(t, e, nil)
			originSessionConfigurePublication(t, e, owner)
			old := e.CatalogRead()
			peer := enginetest.StartSession(t, e, nil)
			op, err := peer.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.Finished {
					scope.Finish(context.Canceled)
				}
			}()
			if err := scope.Gate(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			wait := &originDropWaitContext{Context: op.Context(), entered: make(chan struct{})}
			op.Ctx = wait
			stage := e.BeginCatalogStage(e.CatalogRead())
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("origin drop waiter did not stop")
					}
				}
			}()
			go func() {
				e.Mu.Lock()
				err := (core.DDLHost{E: e, Op: op, CatalogStage: stage}).ClearReplicationOrigin(42)
				e.Mu.Unlock()
				done <- err
			}()
			select {
			case <-wait.entered:
			case err := <-done:
				joined = true
				t.Fatal("internal clear did not wait for the actual owner", err)
			case <-ctx.Done():
				t.Fatal("internal clear did not enter its release wait", ctx.Err())
			}
			// The observer can acquire the engine while the waiter is suspended.
			e.Mu.Lock()
			position, present := e.CopyReplicationOriginProgressLocked()[42]
			e.Mu.Unlock()
			if !present || position != 0 {
				t.Fatal("waiting clear removed its still-owned zero slot")
			}
			switch outcome {
			case "reset":
				originSessionNativeCall(t, e, owner, functions.ReplicationOriginSessionReset)
			case "physical_end":
				owner.RequestEnd(session.CleanClose)
				if err := owner.WaitEnd(ctx); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "replacement":
				source := engine.New()
				if _, err := e.ReplaceCatalog(ctx, source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				if outcome != "cancel" {
					t.Fatal("origin drop did not resume", ctx.Err())
				}
				select {
				case err = <-done:
					joined = true
				case <-time.After(5 * time.Second):
					t.Fatal("canceled origin waiter did not resume")
				}
			}
			if outcome == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if outcome == "replacement" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale DDL continued after native replacement", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			scope.Finish(err)
			if outcome == "cancel" {
				value := originSessionNativeCall(t, e, owner, functions.ReplicationOriginSessionIsSetup)
				if selected, err := value.Boolean(); err != nil || !selected {
					t.Fatal("canceled clear changed physical selection", value, err)
				}
				e.Mu.Lock()
				_, present = e.CopyReplicationOriginProgressLocked()[42]
				e.Mu.Unlock()
				if !present {
					t.Fatal("canceled clear lost slot presence")
				}
			}
			if old.Get(ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: 42}) == nil {
				t.Fatal("retained catalog root changed during release/invalidation")
			}
		})
	}
}
