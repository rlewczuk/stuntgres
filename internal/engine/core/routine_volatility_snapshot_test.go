package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRoutineVolatilitySnapshotOwnership(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead} {
		for _, volatility := range []expr.Volatility{expr.Stable, expr.Immutable, expr.Volatile} {
			t.Run(fmt.Sprintf("isolation_%d/volatility_%d", isolation, volatility), func(t *testing.T) {
				e, _, relation := typedUnitCatalog(t, false)
				op, done := enginetest.RoleOperation(t, e, "postgres")
				enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tablefunc")
				done()
				binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				binder.Clause.AggregateClause = "SELECT"
				raw := enginetest.Statement(t, "SELECT connectby('ignored','k','p','root',0)").(*parser.Select).Targets[0].(parser.FunctionExpr)
				if _, err := binder.BindFunctionProvider(raw); err != nil {
					t.Fatal(err)
				}
				reader, _ := blockingUnitOwner(t, e)
				scope := pgrowlocksUnitScope(t, e, reader)
				scope.Owner.Isolation = isolation
				scope.RoutineDependencies = binder.Dependencies
				ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), relation.Address())
				e.Mu.Lock()
				versions, err := e.Storage.AllocateRowVersionsLocked([][]sqltype.Value{nil}, e.Catalog.State)
				if err == nil {
					e.Visibility.AdvanceCommitSeqLocked()
					versions[0].Born = e.Visibility.CommitSeq()
					e.Storage.Relations[relation.Address()].Versions = versions
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if err := core.ScopeSnapshotDataPhase(scope); err != nil {
					t.Fatal(err)
				}
				scope.Owner.Mu.Lock()
				e.Mu.Lock()
				outer, err := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
				e.Mu.Unlock()
				scope.Owner.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				base := outer.Load()
				e.Mu.Lock()
				replacement := versions[0]
				replacement.Version = e.Storage.AllocateVersionLocked()
				e.Visibility.AdvanceCommitSeqLocked()
				replacement.Born = e.Visibility.CommitSeq()
				e.Storage.Relations[relation.Address()].Versions = []storage.RowVersion{replacement}
				e.Mu.Unlock()
				scope.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{relation.Address(): {replacement}}
				// Query-owned catalog inquiries must survive body cleanup too:
				// refreshing the live actor/settings must not resample the caller.
				callingCatalog := e.CatalogRead()
				scope.Inquiry.CommentCatalog = callingCatalog
				leave := core.ActivateRoutineCommandPolicy(scope, &catalog.Procedure{Volatility: volatility})
				restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				scope.Owner.Mu.Lock()
				e.Mu.Lock()
				local, err := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
				e.Mu.Unlock()
				scope.Owner.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				data := local.Load()
				want := versions[0].Version
				if volatility == expr.Volatile && isolation == session.ReadCommitted {
					want = replacement.Version
				}
				if local == outer || data.Committed[relation.Address()][0].Version != want {
					t.Fatal("command resampled calling roots or failed to acquire a fresh volatile frontier")
				}
				if got := len(data.Own[relation.Address()]); (got != 0) != (volatility == expr.Volatile) {
					t.Fatal("calling private frontier did not distinguish stable and volatile", got)
				}
				// A volatile callee is legal inside a readonly caller and owns
				// a distinct command frontier. Its stable callee must then keep
				// that calling command's already-visible private writes.
				leaveVolatile := core.ActivateRoutineCommandPolicy(scope, &catalog.Procedure{Volatility: expr.Volatile})
				restoreVolatile := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				scope.RoutineDependencies = binder.Dependencies
				scope.Owner.Mu.Lock()
				e.Mu.Lock()
				nested, err := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
				e.Mu.Unlock()
				scope.Owner.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if len(nested.Load().Own[relation.Address()]) != 1 {
					t.Fatal("nested volatile call hid reached writes")
				}
				leaveStable := core.ActivateRoutineCommandPolicy(scope, &catalog.Procedure{Volatility: expr.Stable})
				restoreStable := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				scope.Owner.Mu.Lock()
				e.Mu.Lock()
				stableView, err := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
				e.Mu.Unlock()
				scope.Owner.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if len(stableView.Load().Own[relation.Address()]) != 1 || stableView.Load().Frontier != nested.Load().Frontier {
					t.Fatal("nested stable call lost calling-command frontier")
				}
				if err := restoreStable(nil); err != nil {
					t.Fatal(err)
				}
				leaveStable()
				if stableView.Load() != nil || scope.View != nested {
					t.Fatal("nested stable view leaked")
				}
				if err := restoreVolatile(nil); err != nil {
					t.Fatal(err)
				}
				leaveVolatile()
				if nested.Load() != nil || scope.View != local || local.Load() != data {
					t.Fatal("nested volatile view leaked")
				}
				primary := errors.New("body failure")
				if restore(primary) != primary {
					t.Fatal("restoration replaced primary failure")
				}
				leave()
				if scope.View != outer || outer.Load() != base || local.Load() != nil || scope.OwnWrites != nil {
					t.Fatal("nested cleanup leaked local ownership or changed caller backing")
				}
				if scope.Inquiry.CommentCatalog != callingCatalog {
					t.Fatal("environment refresh discarded the calling query's inquiry snapshot")
				}
				// With policy removed, the existing ordinary lexical activation
				// must again include staged writes, even after a stable failure.
				ordinary := scope.ActivateSQLRoutine(nil, e.CatalogRead())
				if len(scope.OwnWrites[relation.Address()]) != 1 {
					t.Fatal("readonly policy leaked")
				}
				if err := ordinary(nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRoutineProjectedPrivateFrontier(t *testing.T) {
	e, _, relation := typedUnitCatalog(t, false)
	reader, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	target := relation.Address()
	other := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: target.ObjectID + 1}
	scope.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{
		target: {{Row: 1, Version: 1}}, other: {{Row: 2, Version: 2}},
	}
	scope.OwnDeletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{target: {Rows: map[ids.RowID]visibility.DeleteCandidate{1: {Row: 1, Version: 1}}}}
	scope.StageProjectedColumnRoots(target, nil, nil)
	restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
	if len(scope.OwnWrites[target]) != 0 || scope.OwnDeletes[target] != nil || len(scope.OwnWrites[other]) != 1 {
		t.Fatal("nested command resurrected removed projected rows/deletes or lost unrelated private rows")
	}
	scope.Stage.Rows[other] = []storage.RowVersion{{Row: 3, Version: 3}}
	if scope.OwnWrites[other][0].Row != 2 {
		t.Fatal("nested read frontier aliases mutable complete-stage map")
	}
	if err := restore(nil); err != nil { t.Fatal(err) }
	if scope.OwnWrites[target][0].Row != 1 || scope.OwnWrites[other][0].Row != 2 || scope.OwnDeletes[target] == nil {
		t.Fatal("nested cleanup did not restore exact caller frontier")
	}
}
