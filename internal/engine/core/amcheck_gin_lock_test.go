package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestAmcheckGINScopedLocks(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "prior"}[prior], func(t *testing.T) {
			e, d, _ := ginAmcheckUnitRoot(t)
			n := ginAmcheckUnitExpression(t, e)
			reader, ticket := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			heap := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.SQLIndex.Parent)}
			idx := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
			if prior {
				amcheckUnitGrant(t, e, ticket, heap, lock.AccessShare)
				amcheckUnitGrant(t, e, ticket, idx, lock.AccessShare)
			}
			amcheckUnitGrant(t, e, ticket, heap, lock.RowExclusive)
			before := e.Locks.Inspect(ticket)
			if err := core.HostOf(scope).Contrib().Amcheck(n, uint32(d.Address().ObjectID), [3]bool{}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, e.Locks.Inspect(ticket)) {
				t.Fatal("checker changed exact prior contributions")
			}
		})
	}
}
func TestAmcheckGINCaptureReadmission(t *testing.T) {
	for _, action := range []string{"cancel_second", "revoke", "descriptor", "storage", "epoch", "provider", "recreate"} {
		t.Run(action, func(t *testing.T) {
			e, d, _ := ginAmcheckUnitRoot(t)
			var replacement *catalog.Relation
			if action == "recreate" {
				enginetest.Run(t, e, "CREATE INDEX replacement_i ON t USING gin(a,v)")
				replacement, _ = btreeRoot(t, e, "replacement_i")
			}
			if action == "revoke" {
				enginetest.Run(t, e, "CREATE ROLE gin_waiter")
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION gin_index_check(regclass) TO gin_waiter")
			}
			n := ginAmcheckUnitExpression(t, e)
			reader, waiter := blockingUnitOwner(t, e)
			if action == "revoke" {
				enginetest.SessionSQL(t, reader, "SET ROLE gin_waiter")
			}
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			_, holder := blockingUnitOwner(t, e)
			heap := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.SQLIndex.Parent)}
			idx := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())}
			amcheckUnitGrant(t, e, holder, idx, lock.AccessExclusive)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope.Op.Ctx = ctx
			done := make(chan error, 1)
			go func() { done <- core.HostOf(scope).Contrib().Amcheck(n, uint32(d.Address().ObjectID), [3]bool{}) }()
			joined := false
			defer func() {
				cancel()
				if !joined {
					amcheckUnitJoin(t, done)
				}
			}()
			amcheckUnitQueue(t, e, waiter, idx, lock.AccessShare, done)
			if !e.Locks.HasRelationMode(waiter, heap, lock.AccessShare) || e.Locks.HasRelationMode(waiter, idx, lock.AccessShare) {
				t.Fatal("heap must be acquired before index")
			}
			switch action {
			case "cancel_second":
				cancel()
			case "epoch":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "storage":
				e.Mu.Lock()
				next := *e.Storage.Relations[d.SQLIndex.Parent]
				e.Storage.Relations[d.SQLIndex.Parent] = &next
				e.Mu.Unlock()
			case "recreate":
				// Publish a genuine different index under the old spelling while the
				// captured OID is waiting. Readmission must not resolve that name again.
				e.Mu.Lock()
				state := catalog.CloneState(e.Catalog.State)
				state.UnbindName(catalog.DescriptorNameKey(d))
				state.RemoveDescriptor(d.Address())
				state.UnbindName(catalog.DescriptorNameKey(replacement))
				next := *replacement
				next.ObjectName = d.Name()
				next.ObjectRevision++
				state.SetDescriptor(next.Address(), &next)
				state.BindName(catalog.DescriptorNameKey(&next), next.Address())
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
			case "descriptor":
				stage := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(stage)
				next := *d
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				enginetest.PublishStage(t, e, stage)
			case "revoke", "provider":
				e.Mu.Lock()
				state := catalog.CloneState(e.Catalog.State)
				proc := *state.MustProcedure(n.Ref.Address)
				proc.ObjectRevision++
				if action == "revoke" {
					proc.ACL.Explicit = true
					proc.ACL.Entries = nil
				} else {
					proc.Implementation = nil
				}
				state.SetDescriptor(proc.Address(), &proc)
				e.Catalog.PublishLocked(state)
				e.Mu.Unlock()
			}
			if action != "cancel_second" && action != "epoch" {
				e.EndLockOwner(holder)
			}
			err := amcheckUnitJoin(t, done)
			joined = true
			if action == "cancel_second" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if action == "revoke" {
				amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function gin_index_check"})
			} else if err == nil {
				t.Fatal("replacement acquired stale target authority")
			}
			if action == "descriptor" || action == "storage" || action == "epoch" || action == "recreate" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			}
			if e.Locks.HasRelationMode(waiter, heap, lock.AccessShare) || e.Locks.HasRelationMode(waiter, idx, lock.AccessShare) {
				t.Fatal("readmission leaked scoped locks")
			}
		})
	}
}
func TestAmcheckGINCancellation(t *testing.T) {
	for _, where := range []string{"GinBinding.Compare", "GinBinding.Extract"} {
		t.Run(where, func(t *testing.T) {
			e, d, r := ginAmcheckUnitRoot(t)
			n := ginAmcheckUnitExpression(t, e)
			reader, ticket := blockingUnitOwner(t, e)
			enginetest.SessionSQL(t, reader, "SET application_name='gin caller'")
			enginetest.SessionSQL(t, reader, "INSERT INTO t VALUES(7,'{7,7}','cat')")
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			e.Mu.Lock()
			auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			scope.RoutineAuth = auth
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			beforeRole := scope.Op.RoleState()
			beforeInput, beforeOutput := scope.Input, scope.Output
			beforeRoot := ginAmcheckClone(r)
			rows := reader.Transactions.Writes[d.SQLIndex.Parent]
			beforeRows := ginAmcheckCopyWrites(rows)
			beforeLocks := e.Locks.Inspect(ticket)
			active := &amcheckComparisonContext{Context: ctx, cancel: cancel, match: where, work: func() {
				if scope.Op.Restricted == nil {
					t.Fatal("GIN work missing restricted owner")
				}
				e.Mu.Lock()
				e.Mu.Unlock()
				reader.Mu.Lock()
				reader.Mu.Unlock()
			}}
			scope.Op.Ctx = active
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: active, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
			_, err = n.Eval(&evaluation)
			if !active.entered || !errors.Is(err, context.Canceled) {
				t.Fatal("real typed traversal not interrupted", active.entered, err)
			}
			if !reflect.DeepEqual(r, beforeRoot) || !reflect.DeepEqual(rows, beforeRows) || e.Storage.IndexRoots[d.Address()] != r {
				t.Fatal("cancellation mutated retained/private authority")
			}
			if scope.Op.Restricted != nil || scope.Op.RoleState() != beforeRole || reflect.ValueOf(scope.Input.TypeDiagnosticName).Pointer() != reflect.ValueOf(beforeInput.TypeDiagnosticName).Pointer() || !reflect.DeepEqual(scope.Output, beforeOutput) {
				t.Fatal("interruption leaked maintenance presentation")
			}
			if !reflect.DeepEqual(beforeLocks, e.Locks.Inspect(ticket)) {
				t.Fatal("interruption changed prior contributions")
			}
			if value, _ := reader.Setting("application_name"); value != "gin caller" {
				t.Fatal("owner settings escaped", value)
			}
		})
	}
}
