package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgrowlocksVisibilityUnits(t *testing.T) {
	t.Helper()
	t.Run("immutable_visible_prefix_and_own_overlay", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		_, owner := blockingUnitOwner(t, e)
		_, observer := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, owner)
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
		e.mu.Lock()
		allocated, err := e.storage.AllocateRowVersionsLocked([][]sqltype.Value{nil, nil, nil}, e.catalog.state)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		original, inserted := allocated[0], allocated[1]
		original.Born = 1
		successor := original
		successor.Version = allocated[2].Version
		successor.Base = original.Version
		successor.Born = 0
		tuple := lock.MultiTuple{Resource: lock.Resource{ObjectRef: ref, Row: original.Row}, Version: original.Version}
		identityMultiGrant(t, e, owner, tuple, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, owner, tuple, lock.MultiNoKeyAction)
		current := tuple
		current.Version = successor.Version
		// A real successor event has no original locker to copy; subsequently a
		// new actual SELECT lock on that version must not substitute its predecessor.
		if err := e.locks.PhysicalEvents(owner, []lock.MultiEvent{{Tuple: tuple, Mode: lock.MultiNoKeyAction, Successor: successor.Version}}); err != nil {
			t.Fatal(err)
		}
		identityMultiEmit(t, e, owner, current, lock.MultiKeyShare)
		capture := func(data *visibility.Data, want ...ids.VersionID) []lock.PgrowlocksTuple {
			t.Helper()
			view := visibility.NewView(data)
			defer view.Release()
			prefix, err := pgrowlocksPrefix(context.Background(), view, ref)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]ids.VersionID, len(prefix))
			for i, p := range prefix {
				got[i] = p.Tuple.Version
			}
			if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, want) {
				t.Fatal("visible physical versions", got, want)
			}
			return prefix
		}
		data := func() *visibility.Data {
			return &visibility.Data{Tx: observer.Ref(), Frontier: 1, Catalog: e.CatalogRead(), Committed: map[ids.ObjectAddress][]storage.RowVersion{ref.Address: {original}}}
		}
		prefix := capture(data(), original.Version)
		rows, err := e.locks.Pgrowlocks(context.Background(), observer, prefix)
		if err != nil || len(rows) != 1 || rows[0].Tuple.Version != original.Version {
			t.Fatal("observer exact predecessor", rows, err)
		}
		own := data()
		own.Own = map[ids.ObjectAddress][]storage.RowVersion{ref.Address: {successor, inserted}}
		prefix = capture(own, successor.Version, inserted.Version)
		if !prefix[0].PrivateInsertion || !prefix[1].PrivateInsertion {
			t.Fatal("private insertion classification", prefix)
		}
		own.Deleted = map[ids.ObjectAddress]*visibility.PrivateDeletes{ref.Address: {Rows: map[ids.RowID]visibility.DeleteCandidate{original.Row: {Ref: ref, Row: original.Row, Version: successor.Version, Base: original.Version, Own: true}}}}
		capture(own, inserted.Version)
		// Committed end markers act at the captured frontier, not at capture time.
		ended := data()
		ended.Ends = map[ids.ObjectAddress]*storage.DeletionRoot{ref.Address: {Rows: map[ids.RowID]storage.RowEnd{original.Row: {Version: original.Version, End: 2}}}}
		capture(ended, original.Version)
		ended.Frontier = 2
		capture(ended)
		future := data()
		newer := successor
		newer.Base = 0
		newer.Born = 2
		future.Committed[ref.Address] = []storage.RowVersion{newer}
		future.SingleNeedsBirthFilter = true
		capture(future)
	})
	pgrowlocksDynamicSnapshotUnits(t)
}

func pgrowlocksDynamicSnapshotUnits(t *testing.T) {
	t.Helper()
	for _, stable := range []bool{false, true} {
		t.Run(map[bool]string{false: "dynamic_RC_same_statement_roots", true: "dynamic_RR_owner_roots"}[stable], func(t *testing.T) {
			e, _, outer := typedUnitCatalog(t, false)
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			e.mu.Lock()
			target, err := table.NewRelation(ddlHost{e: e}, outer.Namespace(), "pgrow_dynamic", outer.Owner, nil, nil)
			if err == nil {
				err = e.stageRowTypeLocked(stage, target)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.Create(target); err != nil {
				t.Fatal(err)
			}
			domainUnitPublish(t, e, stage)
			pgrowlocksUnitInstalled(t, e)
			call := pgrowlocksUnitExpression(t, e)
			reader, _ := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, reader)
			scope.routineDependencies = []catalog.ExpressionDependency{{Ref: call.Ref, Revision: call.Procedure.Revision()}}
			if stable {
				scope.owner.Isolation = session.RepeatableRead
			}
			targetRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), target.Address())
			outerRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), outer.Address())
			e.mu.Lock()
			versions, err := e.storage.AllocateRowVersionsLocked([][]sqltype.Value{nil}, e.catalog.state)
			if err == nil {
				e.visibility.AdvanceCommitSeqLocked()
				versions[0].Born = e.visibility.CommitSeq()
				e.storage.Relations[target.Address()].Versions = versions
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.snapshotDataPhase(); err != nil {
				t.Fatal(err)
			}
			scope.owner.Mu.Lock()
			e.mu.Lock()
			first, err := scope.acquireDataViewLocked(e, outerRef)
			e.mu.Unlock()
			scope.owner.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			frontier := first.Load().Frontier
			e.mu.Lock()
			replacement := versions[0]
			replacement.Version = e.storage.AllocateVersionLocked()
			e.visibility.AdvanceCommitSeqLocked()
			replacement.Born = e.visibility.CommitSeq()
			e.storage.Relations[target.Address()].Versions = []storage.RowVersion{replacement}
			e.mu.Unlock()
			scope.owner.Mu.Lock()
			e.mu.Lock()
			local, err := scope.captureDataViewsAtLocked(e, []ids.ObjectRef{targetRef}, false, first.Load())
			e.mu.Unlock()
			scope.owner.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer local.Release()
			prefix, err := pgrowlocksPrefix(context.Background(), local, targetRef)
			if err != nil || len(prefix) != 1 || prefix[0].Tuple.Version != versions[0].Version || local.Load().Frontier != frontier || scope.view != first {
				t.Fatal("dynamic target resampled frontier/root or replaced outer view", prefix, err)
			}
			if _, ok := first.Load().Committed[target.Address()]; ok {
				t.Fatal("local capture mutated outer view")
			}
		})
	}
}
