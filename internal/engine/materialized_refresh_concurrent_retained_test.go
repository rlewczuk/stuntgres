package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
)

func TestMaterializedRefreshConcurrentRetainedProvenance(t *testing.T) {
	for _, sequence := range [][]bool{{true, true}, {true, false, true}, {false, true}} {
		e := New()
		d := concurrentUnitRelation(t, e)
		original := e.Storage.Relations[d.Address()]
		changes := session.NewJournal()
		previous := original
		for i, concurrent := range sequence {
			replacement := &storage.Relation{Generation: previous.Generation + 1}
			stage := &core.CatalogStage{MaterializedSlot: &view.MaterializedStage{Replacement: &view.MaterializedReplacement{Original: d, Descriptor: d, Predecessor: previous, Storage: replacement, Generation: previous.Generation, Ends: previous.Ends, Concurrent: concurrent}}}
			e.Mu.Lock()
			core.EngineStageCatalogChangesLocked(e, stage, changes)
			e.Mu.Unlock()
			shape := changes.Storage[d.Address()]
			continuous := true
			for _, mode := range sequence[:i+1] {
				continuous = continuous && mode
			}
			if shape.Original != original || shape.Replacement != replacement || shape.MaterializedConcurrent != concurrent || shape.MaterializedContinuous != continuous {
				t.Fatal("lost first predecessor or interrupted provenance", sequence, i, shape)
			}
			if original.MaterializedLineage != nil || replacement.MaterializedLineage != nil {
				t.Fatal("private stage stamped committed lineage")
			}
			previous = replacement
		}
	}
	e := New()
	d := concurrentUnitRelation(t, e)
	private := &storage.Relation{}
	changes := session.NewJournal()
	changes.Storage[d.Address()] = session.StorageChange{Replacement: private, Relation: d}
	stage := &core.CatalogStage{MaterializedSlot: &view.MaterializedStage{Replacement: &view.MaterializedReplacement{Original: d, Descriptor: d, Predecessor: private, Storage: &storage.Relation{}, Concurrent: true}}}
	e.Mu.Lock()
	core.EngineStageCatalogChangesLocked(e, stage, changes)
	e.Mu.Unlock()
	if shape := changes.Storage[d.Address()]; shape.Original != nil || shape.MaterializedContinuous || !shape.MaterializedConcurrent {
		t.Fatal("private CREATE acquired committed provenance", shape)
	}
}

func TestMaterializedRefreshConcurrentProducerSourceFences(t *testing.T) {
	e := New()
	old := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, old, "unit_u", "i", "")
	old = e.Catalog.State.MustRelation(old.Address())
	old = concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 1), false, true)
	catalog := e.CatalogRead()
	stored := e.Storage.Relations[old.Address()]
	snapshot := &visibility.Data{Catalog: catalog, Committed: map[ids.ObjectAddress][]storage.RowVersion{old.Address(): stored.Versions}, SingleGeneration: stored}
	fence := core.NewMaterializedSourceFence(core.MaterializedSourceFenceFields{Ref: e.Catalog.Domain.Ref(catalog.Epoch(), old.Address()), Relation: old, Storage: stored})
	current := concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 2), true, true)
	e.Mu.Lock()
	if !view.MaterializedSourceCurrent(snapshot, old, e.Catalog.State, e.Storage.Relations[old.Address()]) || !core.EngineMaterializedSourceFenceCurrent(e, fence) {
		t.Fatal("compatible publication rejected captured source")
	}
	changes := session.NewJournal()
	changes.Fences = []session.Fence{fence}
	clone := changes.Clone()
	if err := e.CatalogPreflightLocked(clone); err != nil {
		t.Fatal("source-only retained fence rejected", err)
	}
	changes.Originals[old.Address()] = old
	if err := e.CatalogPreflightLocked(changes); err == nil {
		t.Fatal("source proof weakened an exact original fence")
	}
	changed := *current
	changed.ObjectName = "renamed"
	e.Catalog.State.SetDescriptor(current.Address(), &changed)
	if core.EngineMaterializedSourceFenceCurrent(e, fence) || view.MaterializedSourceCurrent(snapshot, old, e.Catalog.State, e.Storage.Relations[old.Address()]) {
		t.Fatal("source definition retargeted")
	}
	e.Catalog.State.SetDescriptor(current.Address(), current)
	e.Mu.Unlock()
	current = concurrentUnitReplace(t, e, current, refreshUnitRows(t, current, 3), false, true)
	e.Mu.Lock()
	if core.EngineMaterializedSourceFenceCurrent(e, fence) || view.MaterializedSourceCurrent(snapshot, old, e.Catalog.State, e.Storage.Relations[current.Address()]) {
		t.Fatal("plain rewrite bridged active source snapshot")
	}
	e.Mu.Unlock()
	// Snapshot vectors remain the old immutable data, not the newly installed bag.
	if len(snapshot.Committed[old.Address()]) != 1 {
		t.Fatal("source snapshot was recaptured")
	}
}
