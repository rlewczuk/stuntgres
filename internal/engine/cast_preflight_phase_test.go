package engine

import (
	"context"
	"errors"
	"testing"

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
			ctx.Context = op.ctx
			op.ctx = ctx
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
				if len(scope.querySources) != 1 || scope.querySources[0].ref.Domain() != e.catalog.domain || scope.querySources[0].relation == nil {
					t.Error("preflight lost locked source occurrence identity")
					return // Keep the assertion non-panicking inside execution.
				}
				switch mode {
				case "authority":
					op.session.mu.Lock()
					op.session.authorization.state.CurrentUser = actor.Ref
					op.session.mu.Unlock()
				case "teardown":
					op.session.RequestEnd(CleanClose)
				case "descriptor":
					leaf := scope.querySources[0]
					address := leaf.ref.Address
					original := e.catalog.state.Get(address)
					replacement := *leaf.relation
					// Compatible descriptor copies are admissible; invalidate the
					// captured physical attribute identity instead.
					replacement.Attributes = append([]int32(nil), replacement.Attributes...)
					replacement.Attributes[0]++
					e.catalog.state.setDescriptor(address, &replacement)
					restore = func() { e.catalog.state.setDescriptor(address, original) }
				case "epoch":
					original := e.catalog.state.epoch
					e.catalog.state.epoch++
					restore = func() { e.catalog.state.epoch = original }
				}
			}
			result, executeErr := scope.Execute(statement(t, "SELECT int4($1::text) FROM t"), 1, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
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
