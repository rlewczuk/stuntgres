package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// The importer arms this context after analysis/import. The first following
// cancellation checkpoint is preflight entry. TryLock observes lock ownership
// deterministically, without sleeps, goroutines, or a production test hook.
type castPreflightContext struct {
	context.Context
	e        *Engine
	t        *testing.T
	armed    bool
	entered  bool
	cancelAt string
	visit    func()
}

func (c *castPreflightContext) Err() error {
	if !c.armed {
		return c.Context.Err()
	}
	if !c.e.Mu.TryLock() {
		if !c.entered {
			c.t.Error("SELECT preflight entered with Engine.mu held")
			return context.Canceled
		}
		if c.cancelAt == "relock" {
			return context.Canceled
		}
		return c.Context.Err()
	}
	defer c.e.Mu.Unlock()
	if !c.entered {
		c.entered = true
		c.visit()
	}
	if c.cancelAt == "preflight" {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestCastCoercionSelectPreflightAuthorityIdentityCancellation(t *testing.T) {
	for _, mode := range []string{"authority", "descriptor", "epoch", "preflight", "relock", "teardown", "success"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(i INT4)")
			run(t, e, "INSERT INTO t VALUES(7)")
			run(t, e, "CREATE ROLE actor LOGIN")
			actor, _ := e.Roles().Lookup("actor")
			ctx := &castPreflightContext{Context: context.Background(), e: e, t: t, cancelAt: mode}
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			// Acquire wraps caller cancellation; expose deterministic Err
			// checkpoints directly while retaining the operation lifecycle.
			ctx.Context = op.Ctx
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			// Finish before ending the operation even if an assertion panics:
			// session teardown waits for this statement's admission to release.
			defer func() {
				ctx.armed = false
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			var restore func()
			ctx.visit = func() {
				if scope.View != nil {
					t.Error("source view captured before preflight/ACL")
				}
				if len(scope.QuerySources) != 1 || scope.QuerySources[0].Ref.Domain() != e.Catalog.Domain || scope.QuerySources[0].Relation == nil {
					t.Error("preflight lost locked source occurrence identity")
					return // Keep the assertion non-panicking inside execution.
				}
				switch mode {
				case "authority":
					op.Session.Mu.Lock()
					op.Session.Authorization.State.CurrentUser = actor.Ref
					op.Session.Mu.Unlock()
				case "teardown":
					op.Session.RequestEnd(session.CleanClose)
				case "descriptor":
					leaf := scope.QuerySources[0]
					address := leaf.Ref.Address
					original := e.Catalog.State.Get(address)
					replacement := *leaf.Relation
					// Compatible descriptor copies are admissible; invalidate the
					// captured physical attribute identity instead.
					replacement.Attributes = append([]int32(nil), replacement.Attributes...)
					replacement.Attributes[0]++
					e.Catalog.State.SetDescriptor(address, &replacement)
					restore = func() { e.Catalog.State.SetDescriptor(address, original) }
				case "epoch":
					original := e.Catalog.State.Epoch()
					e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
					restore = func() { e.Catalog.State.SetEpoch(original) }
				}
			}
			result, executeErr := scope.Execute(statement(t, "SELECT int4($1::text) FROM t"), 1, func(c context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				value, err := signature.Types[0].Type.Input(c, "42", -1)
				if err != nil {
					return err
				}
				frame[0] = value
				ctx.armed = true
				return nil
			})
			ctx.armed = false
			if restore != nil {
				e.Mu.Lock()
				restore()
				e.Mu.Unlock()
			}
			if !ctx.entered {
				t.Error("preflight checkpoint never entered")
			}
			switch mode {
			case "authority":
				state(t, executeErr, "42501")
			case "descriptor", "epoch":
				if !errors.Is(executeErr, ids.ErrCatalogReplaced) {
					t.Error(executeErr)
				}
			case "preflight", "relock", "teardown":
				if !errors.Is(executeErr, context.Canceled) {
					t.Error(executeErr)
				}
			case "success":
				if executeErr != nil || result.Rows == nil || result.Count != 1 {
					t.Error(result, executeErr)
				}
			}
			if mode != "success" && (result.Rows != nil || scope.View != nil) {
				t.Error("failed preflight/authority acquired a source or cursor")
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error("scope teardown changed failure", err, executeErr)
			}
			if scope.View != nil || len(scope.QuerySources) != 0 || scope.LockedDescriptor != nil || scope.LockTicket != nil || !scope.Finished {
				t.Error("statement resources survived teardown")
			}
		})
	}
}
