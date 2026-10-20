package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
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
	if !c.e.mu.TryLock() {
		if !c.entered {
			c.t.Error("SELECT preflight entered with Engine.mu held")
			return context.Canceled
		}
		if c.cancelAt == "relock" {
			return context.Canceled
		}
		return c.Context.Err()
	}
	defer c.e.mu.Unlock()
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
				if !scope.finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			var restore func()
			ctx.visit = func() {
				if scope.view != nil {
					t.Error("source view captured before preflight/ACL")
				}
				if len(scope.querySources) != 1 || scope.querySources[0].Ref.Domain() != e.catalog.Domain || scope.querySources[0].Relation == nil {
					t.Error("preflight lost locked source occurrence identity")
					return // Keep the assertion non-panicking inside execution.
				}
				switch mode {
				case "authority":
					op.Session.mu.Lock()
					op.Session.authorization.state.CurrentUser = actor.Ref
					op.Session.mu.Unlock()
				case "teardown":
					op.Session.RequestEnd(CleanClose)
				case "descriptor":
					leaf := scope.querySources[0]
					address := leaf.Ref.Address
					original := e.catalog.state.Get(address)
					replacement := *leaf.Relation
					// Compatible descriptor copies are admissible; invalidate the
					// captured physical attribute identity instead.
					replacement.Attributes = append([]int32(nil), replacement.Attributes...)
					replacement.Attributes[0]++
					e.catalog.state.SetDescriptor(address, &replacement)
					restore = func() { e.catalog.state.SetDescriptor(address, original) }
				case "epoch":
					original := e.catalog.state.Epoch()
					e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
					restore = func() { e.catalog.state.SetEpoch(original) }
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
				e.mu.Lock()
				restore()
				e.mu.Unlock()
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
			if mode != "success" && (result.Rows != nil || scope.view != nil) {
				t.Error("failed preflight/authority acquired a source or cursor")
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err := scope.Finish(executeErr); err != executeErr {
				t.Error("scope teardown changed failure", err, executeErr)
			}
			if scope.view != nil || len(scope.querySources) != 0 || scope.lockedDescriptor != nil || scope.ticket != nil || !scope.finished {
				t.Error("statement resources survived teardown")
			}
		})
	}
}
