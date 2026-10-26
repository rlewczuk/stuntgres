package core_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func amcheckUnitGrant(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode) {
	t.Helper()
	request, err := e.Locks.StartAcquire(context.Background(), ticket, resource, mode, lock.Policy{})
	if err != nil || request != nil {
		t.Fatal("unexpected queued setup", request, err)
	}
}
func amcheckUnitQueue(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		state := e.Locks.Inspect(ticket).Resources[resource]
		if state != nil {
			for _, waiter := range state.Pending {
				if waiter.Ticket == ticket && waiter.Mode == mode && !waiter.Terminal {
					return
				}
			}
		}
		select {
		case err := <-done:
			t.Fatal("call finished before real queue admission", err)
		case <-ctx.Done():
			t.Fatal("missing real queue", ctx.Err())
		default:
			runtime.Gosched()
		}
	}
}
func amcheckUnitJoin(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("amcheck worker did not join")
		return nil
	}
}
func TestAmcheckScopedLocks(t *testing.T) {
	for _, parent := range []bool{false, true} {
		for _, prior := range []bool{false, true} {
			t.Run(map[bool]string{false: "basic", true: "parent"}[parent]+map[bool]string{false: "/new", true: "/prior"}[prior], func(t *testing.T) {
				e, d, _ := amcheckUnitRoot(t, false)
				n := amcheckUnitExpression(t, e, parent)
				reader, ticket := blockingUnitOwner(t, e)
				scope := pgrowlocksUnitScope(t, e, reader)
				scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
				heap := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.SQLIndex.Parent)}
				index := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
				mode := lock.AccessShare
				if parent {
					mode = lock.Share
				}
				if prior {
					amcheckUnitGrant(t, e, ticket, heap, mode)
					amcheckUnitGrant(t, e, ticket, index, mode)
				}
				amcheckUnitGrant(t, e, ticket, heap, lock.RowExclusive)
				before := e.Locks.Inspect(ticket)
				if err := core.HostOf(scope).Amcheck(n, uint32(d.Address().ObjectID), [3]bool{true, true, true}); err != nil {
					t.Fatal(err)
				}
				after := e.Locks.Inspect(ticket)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("function changed exact prior contributions", before, after)
				}
			})
		}
	}
}
func TestAmcheckCaptureReadmission(t *testing.T) {
	for _, action := range []string{"cancel_second", "revoke", "replace_descriptor", "replace_epoch"} {
		t.Run(action, func(t *testing.T) {
			e, d, _ := amcheckUnitRoot(t, false)
			if action == "revoke" {
				enginetest.Run(t, e, "CREATE ROLE am_reader")
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION bt_index_parent_check(regclass) TO am_reader")
			}
			n := amcheckUnitExpression(t, e, true)
			reader, waiter := blockingUnitOwner(t, e)
			if action == "revoke" {
				sessionSQL(t, reader, "SET ROLE am_reader")
			}
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			_, holder := blockingUnitOwner(t, e)
			heap := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.SQLIndex.Parent)}
			indexResource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
			amcheckUnitGrant(t, e, holder, indexResource, lock.AccessExclusive)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope.Op.Ctx = ctx
			done := make(chan error, 1)
			go func() { done <- core.HostOf(scope).Amcheck(n, uint32(d.Address().ObjectID), [3]bool{true, true, true}) }()
			joined := false
			defer func() {
				cancel()
				if !joined {
					amcheckUnitJoin(t, done)
				}
			}()
			amcheckUnitQueue(t, e, waiter, indexResource, lock.Share, done)
			if !e.Locks.HasRelationMode(waiter, heap, lock.Share) || e.Locks.HasRelationMode(waiter, indexResource, lock.Share) {
				t.Fatal("heap/index acquisition order")
			}
			switch action {
			case "cancel_second":
				cancel()
			case "replace_epoch":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "replace_descriptor":
				stage := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(stage)
				next := *d
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				domainUnitPublish(t, e, stage)
			case "revoke":
				// Fresh EXECUTE is checked after the genuine wait. A distinct
				// actor avoids superuser's intentional ACL bypass.
				state := catalog.CloneState(e.Catalog.State)
				proc := *state.MustProcedure(n.Ref.Address)
				proc.ObjectRevision++
				proc.ACL.Explicit = true
				proc.ACL.Entries = nil
				state.SetDescriptor(proc.Address(), &proc)
				e.Mu.Lock()
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
			}
			if action != "cancel_second" && action != "replace_epoch" {
				e.EndLockOwner(holder)
			}
			err := amcheckUnitJoin(t, done)
			joined = true
			if action == "cancel_second" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if action == "replace_descriptor" || action == "replace_epoch" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("retargeted stale relation", err)
				}
			}
			if action == "revoke" {
				amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function bt_index_parent_check"})
			}
			if e.Locks.HasRelationMode(waiter, heap, lock.Share) || e.Locks.HasRelationMode(waiter, indexResource, lock.Share) {
				t.Fatal("wait/error leaked function contribution")
			}
		})
	}
}
