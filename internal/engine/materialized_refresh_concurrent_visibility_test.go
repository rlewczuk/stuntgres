package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestMaterializedRefreshConcurrentReaderAdmission(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.address)
	original := e.storage[d.address]
	scope := refreshUnitScope(t, e, context.Background())
	ref := e.catalog.domain.Ref(e.catalog.state.epoch, d.address)
	scope.querySources = []queryLeaf{{ref: ref, relation: d, storage: original}, {ref: ref, relation: d, storage: original}}
	d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
	current := e.storage[d.address]
	e.mu.Lock()
	err := scope.refreshRelationGrantsLocked(e)
	e.mu.Unlock()
	if err != nil {
		t.Fatal("admission-to-publication recapture", err)
	}
	for _, source := range scope.querySources {
		if source.relation != d || source.storage != current {
			t.Fatal("aliases did not recapture one coherent manifest")
		}
	}
	e.mu.Lock()
	view, err := scope.acquireDataViewsLocked(e, []ids.ObjectRef{ref, ref})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := view.read()
	frontier := retained.frontier
	if !sameVersionRoot(retained.committed[d.address], current.versions) {
		t.Fatal("capture did not own current immutable root")
	}
	next := concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 2), true, true)
	e.mu.Lock()
	err = scope.refreshRelationGrantsLocked(e)
	if err == nil {
		err = scope.validateLockedRelation(e)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal("captured reader rejected compatible publication", err)
	}
	again, err := scope.AcquireDataView(ref)
	if err != nil || again != view {
		t.Fatal("captured data view changed", err)
	}
	if again.read().frontier != frontier || !sameVersionRoot(again.read().committed[d.address], current.versions) || len(again.read().committed[d.address]) != 4 {
		t.Fatal("reader mixed incarnations")
	}
	for _, source := range scope.querySources {
		if source.relation != d || source.storage != current {
			t.Fatal("postcapture layout/storage was recaptured")
		}
	}
	// Shared token alone cannot authorize a metadata/owner rewrite.
	incompatible := *next
	incompatible.name = "renamed"
	e.mu.Lock()
	e.catalog.state.descriptors[next.address] = &incompatible
	err = scope.validateLockedRelation(e)
	e.mu.Unlock()
	if !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("defining authority fence weakened", err)
	}
}

func TestMaterializedRefreshConcurrentSnapshots(t *testing.T) {
	for _, beforeCapture := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured_first", true: "database_snapshot_before_first_demand"}[beforeCapture], func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.catalog.state.MustRelation(d.address)
			stage, unrelated := queryCreateStage(t, e, "unrelated_snapshot", 1)
			queryCreatePublish(t, e, stage)
			original := e.storage[d.address]
			original.ends = &deletionRoot{}
			scope := refreshUnitScope(t, e, context.Background())
			scope.owner.isolation = RepeatableRead
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			root := scope.owner.roots[d.address]
			frontier := scope.owner.frontier
			if root.storage != original || root.ends != original.ends || scope.owner.roots[unrelated.address].storage != e.storage[unrelated.address] {
				t.Fatal("snapshot did not capture database roots")
			}
			ref := e.catalog.domain.Ref(e.catalog.state.epoch, d.address)
			if !beforeCapture {
				if _, err := scope.AcquireDataView(ref); err != nil {
					t.Fatal(err)
				}
			}
			for _, count := range []int{4, 1} {
				d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, count), true, true)
				if scope.view != nil {
					scope.view.Release()
					scope.view = nil
				}
				view, err := scope.AcquireDataView(ref)
				if err != nil {
					t.Fatal("RR compatible root selection", err)
				}
				data := view.read()
				if data.frontier != frontier || !sameVersionRoot(data.committed[d.address], root.versions) || data.ends[d.address] != root.ends || data.needsBirthFilterAt(d.address) || len(data.committed[d.address]) != 3 {
					t.Fatal("RR selected current/birth-filtered rows")
				}
			}
		})
	}
	for _, phase := range []string{"plain_data", "plain_no_data", "control_epoch", "ordinary_incarnation", "owner", "schema", "name", "view", "columns", "attributes", "rowtype", "ordinary_table"} {
		t.Run("reject_"+phase, func(t *testing.T) {
			e := New()
			d := refreshUnitRelation(t, e)
			materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
			d = e.catalog.state.MustRelation(d.address)
			scope := refreshUnitScope(t, e, context.Background())
			scope.owner.isolation = RepeatableRead
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			originalEpoch := e.catalog.state.epoch
			defer func() { e.catalog.state.epoch = originalEpoch }()
			ref := e.catalog.domain.Ref(originalEpoch, d.address)
			d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 4), true, true)
			switch phase {
			case "plain_data":
				d = concurrentUnitReplace(t, e, d, refreshUnitRows(t, d, 2), false, true)
			case "plain_no_data":
				d = concurrentUnitReplace(t, e, d, nil, false, false)
			case "control_epoch":
				e.catalog.state.epoch++
			case "ordinary_incarnation":
				copy := *e.storage[d.address]
				copy.materializedLineage = nil
				e.storage[d.address] = &copy
			default:
				copy := *d
				switch phase {
				case "owner":
					copy.owner = authorization.PrincipalRef{}
				case "schema":
					copy.namespace.ObjectID++
				case "name":
					copy.name = "renamed"
				case "view":
					definition := *copy.view
					copy.view = &definition
				case "columns":
					copy.columns = append([]ColumnDescriptor(nil), copy.columns...)
				case "attributes":
					copy.attributes = append([]int32(nil), copy.attributes...)
				case "rowtype":
					copy.rowType++
				case "ordinary_table":
					copy.relationKind = tableRelation
				}
				e.catalog.state.descriptors[d.address] = &copy
			}
			view, err := scope.AcquireDataView(ref)
			if view != nil || !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("incompatible snapshot replacement bridged lineage", phase, err)
			}
		})
	}
}

func TestMaterializedRefreshConcurrentLateCancellation(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, d, "unit_u", "i", "")
	d = e.catalog.state.MustRelation(d.address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := refreshUnitScope(t, e, ctx)
	stmt := &parser.RefreshMaterializedView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, Concurrent: parser.Position{Character: 27}}
	if err := scope.acquireMaterializedRefresh(stmt); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	_, err = e.refreshMaterializedLocked(scope, auth, stmt, 0)
	if err != nil {
		t.Fatal(err)
	}
	frontier := e.commitSeq
	published := e.storage[d.address]
	if !scope.completed || len(published.versions) != 1 {
		t.Fatal("actual refresh did not complete publication")
	}
	cancel()
	if err := scope.Finish(nil); err != nil {
		t.Fatal("late cancellation retracted completed utility", err)
	}
	if e.storage[d.address] != published || e.commitSeq != frontier {
		t.Fatal("late completion published twice")
	}
}
