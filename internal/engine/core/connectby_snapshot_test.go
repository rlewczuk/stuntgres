package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestConnectbyNestedSourceSnapshotOwnership(t *testing.T) {
	for _, stable := range []bool{false, true} {
		name := "read_committed"
		if stable {
			name = "repeatable_read"
		}
		t.Run(name, func(t *testing.T) {
			e, _, outer := typedUnitCatalog(t, false)
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			e.Mu.Lock()
			target, err := table.NewRelation(core.DDLHost{E: e}, outer.Namespace(), "cb_dynamic_target", outer.Owner, nil, nil)
			if err == nil {
				target.TableMethod = 2
				err = e.StageRowTypeLocked(stage, target)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.Create(target); err != nil {
				t.Fatal(err)
			}
			domainUnitPublish(t, e, stage)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tablefunc")
			done()
			binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			binder.Clause.AggregateClause = "SELECT"
			raw := enginetest.Statement(t, "SELECT connectby('cb_dynamic_target','k','p','root',0)").(*parser.Select).Targets[0].(parser.FunctionExpr)
			bound, err := binder.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			call := bound.(plan.InstalledConnectbyExpr)
			reader, _ := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.RoutineDependencies = binder.Dependencies
			if stable {
				scope.Owner.Isolation = session.RepeatableRead
			}
			targetRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), target.Address())
			outerRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), outer.Address())
			e.Mu.Lock()
			versions, err := e.Storage.AllocateRowVersionsLocked([][]sqltype.Value{nil}, e.Catalog.State)
			if err == nil {
				e.Visibility.AdvanceCommitSeqLocked()
				versions[0].Born = e.Visibility.CommitSeq()
				e.Storage.Relations[target.Address()].Versions = versions
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
			first, err := core.ScopeAcquireDataViewLocked(scope, e, outerRef)
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			base := first.Load()
			retained, present := base.DynamicRoots[target.Address()]
			if _, captured := base.Committed[target.Address()]; captured || !present || len(retained.Versions) != 1 || retained.Versions[0].Version != versions[0].Version {
				t.Fatal("outer source must retain, but not acquire, the dynamic target", call.Ref)
			}
			e.Mu.Lock()
			replacement := versions[0]
			replacement.Version = e.Storage.AllocateVersionLocked()
			e.Visibility.AdvanceCommitSeqLocked()
			replacement.Born = e.Visibility.CommitSeq()
			e.Storage.Relations[target.Address()].Versions = []storage.RowVersion{replacement}
			e.Mu.Unlock()
			restore := core.ActivateConnectbySource(scope, e.CatalogRead())
			restored := false
			defer func() {
				if !restored {
					_ = restore(nil)
				}
			}()
			if scope.View != nil {
				t.Fatal("nested activation borrowed outer source membership")
			}
			// Policy preparation enters through the root-only capture seam before
			// ordinary source acquisition. It must install the same local owner.
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			local, err := core.CaptureConnectbySourceRoots(scope, e, []ids.ObjectRef{targetRef})
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			data := local.Load()
			if scope.View != local || local == first || data.Frontier != base.Frontier || len(data.Committed[target.Address()]) != 1 || data.Committed[target.Address()][0].Version != versions[0].Version {
				t.Fatal("nested capture replaced ownership or resampled the retained root")
			}
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			reused, reuseErr := core.ScopeAcquireDataViewLocked(scope, e, targetRef)
			_, outsideErr := core.ScopeAcquireDataViewLocked(scope, e, outerRef)
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if reuseErr != nil || reused != local || outsideErr == nil {
				t.Fatal("nested membership validation recaptured or widened the local view", reuseErr, outsideErr)
			}
			if err := restore(nil); err != nil {
				t.Fatal(err)
			}
			restored = true
			if scope.View != first || first.Load() != base || local.Load() != nil {
				t.Fatal("restoration failed to release local ownership and retain outer ownership")
			}
			if _, captured := base.Committed[target.Address()]; captured {
				t.Fatal("nested acquisition mutated immutable outer membership")
			}
			// Explicit-base and latest captures must not be redirected through
			// the borrowed sourceBase, even while connectby activation is live.
			restoreVariants := core.ActivateConnectbySource(scope, e.CatalogRead())
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			explicit, explicitErr := scope.CaptureDataViewsAtLocked(e, []ids.ObjectRef{targetRef}, false, base)
			latest, latestErr := scope.CaptureDataViewsAtLocked(e, []ids.ObjectRef{targetRef}, true, nil)
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if explicitErr != nil || latestErr != nil {
				_ = restoreVariants(nil)
				t.Fatal(explicitErr, latestErr)
			}
			if scope.View != nil || explicit.Load().Committed[target.Address()][0].Version != versions[0].Version || latest.Load().Committed[target.Address()][0].Version != replacement.Version {
				t.Fatal("explicit-base/latest capture was redirected or installed as ordinary source")
			}
			explicit.Release() // Explicit-base capture remains caller-owned.
			if err := restoreVariants(nil); err != nil {
				t.Fatal(err)
			}
			if latest.Load() != nil || scope.View != first || first.Load() != base {
				t.Fatal("latest validation owner leaked or outer ownership changed")
			}
			// A normal nested SQL activation inside connectby must start with
			// default demand, not inherit the runtime-named sourceBase.
			restoreConnectby := core.ActivateConnectbySource(scope, e.CatalogRead())
			restoreOrdinary := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			ordinary, ordinaryErr := core.CaptureConnectbySourceRoots(scope, e, []ids.ObjectRef{targetRef})
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if ordinaryErr != nil {
				t.Fatal(ordinaryErr)
			}
			if ordinary.Load().DynamicRoots != nil {
				t.Fatal("unrelated activation inherited connectby roots")
			}
			if err := restoreOrdinary(nil); err != nil {
				t.Fatal(err)
			}
			if ordinary.Load() != nil {
				t.Fatal("ordinary local owner leaked")
			}
			if err := restoreConnectby(nil); err != nil {
				t.Fatal(err)
			}
			if scope.View != first || first.Load() != base {
				t.Fatal("nested ordinary activation lost outer ownership")
			}
		})
	}
}
