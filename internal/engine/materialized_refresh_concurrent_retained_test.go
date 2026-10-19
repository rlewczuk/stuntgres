package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestMaterializedRefreshConcurrentRetainedProvenance(t *testing.T) {
	for _, sequence := range [][]bool{{true, true}, {true, false, true}, {false, true}} {
		e := New()
		d := concurrentUnitRelation(t, e)
		original := e.storage.Relations[d.Address()]
		changes := newCatalogChanges()
		previous := original
		for i, concurrent := range sequence {
			replacement := &storage.Relation{Generation: previous.Generation + 1}
			stage := &CatalogStage{materializedReplacement: &materializedReplacement{original: d, descriptor: d, predecessor: previous, storage: replacement, generation: previous.Generation, ends: previous.Ends, concurrent: concurrent}}
			e.mu.Lock()
			e.stageCatalogChangesLocked(stage, changes)
			e.mu.Unlock()
			shape := changes.storage[d.Address()]
			continuous := true
			for _, mode := range sequence[:i+1] {
				continuous = continuous && mode
			}
			if shape.original != original || shape.replacement != replacement || shape.materializedConcurrent != concurrent || shape.materializedContinuous != continuous {
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
	changes := newCatalogChanges()
	changes.storage[d.Address()] = catalogStorageChange{replacement: private, relation: d}
	stage := &CatalogStage{materializedReplacement: &materializedReplacement{original: d, descriptor: d, predecessor: private, storage: &storage.Relation{}, concurrent: true}}
	e.mu.Lock()
	e.stageCatalogChangesLocked(stage, changes)
	e.mu.Unlock()
	if shape := changes.storage[d.Address()]; shape.original != nil || shape.materializedContinuous || !shape.materializedConcurrent {
		t.Fatal("private CREATE acquired committed provenance", shape)
	}
}

func TestMaterializedRefreshConcurrentProducerSourceFences(t *testing.T) {
	e := New()
	old := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, old, "unit_u", "i", "")
	old = e.catalog.state.MustRelation(old.Address())
	old = concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 1), false, true)
	catalog := e.CatalogRead()
	stored := e.storage.Relations[old.Address()]
	snapshot := &visibility.Data{Catalog: catalog, Committed: map[ids.ObjectAddress][]storage.RowVersion{old.Address(): stored.Versions}, SingleGeneration: stored}
	fence := materializedSourceFence{ref: e.catalog.Domain.Ref(catalog.Epoch(), old.Address()), relation: old, storage: stored}
	current := concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 2), true, true)
	e.mu.Lock()
	if !materializedSourceCurrent(snapshot, old, e.catalog.state, e.storage.Relations[old.Address()]) || !e.materializedSourceFenceCurrent(fence) {
		t.Fatal("compatible publication rejected captured source")
	}
	changes := newCatalogChanges()
	changes.materializedSources = []materializedSourceFence{fence}
	clone := cloneCatalogChanges(changes)
	if err := e.catalogPreflightLocked(clone); err != nil {
		t.Fatal("source-only retained fence rejected", err)
	}
	changes.originals[old.Address()] = old
	if err := e.catalogPreflightLocked(changes); err == nil {
		t.Fatal("source proof weakened an exact original fence")
	}
	changed := *current
	changed.ObjectName = "renamed"
	e.catalog.state.SetDescriptor(current.Address(), &changed)
	if e.materializedSourceFenceCurrent(fence) || materializedSourceCurrent(snapshot, old, e.catalog.state, e.storage.Relations[old.Address()]) {
		t.Fatal("source definition retargeted")
	}
	e.catalog.state.SetDescriptor(current.Address(), current)
	e.mu.Unlock()
	current = concurrentUnitReplace(t, e, current, refreshUnitRows(t, current, 3), false, true)
	e.mu.Lock()
	if e.materializedSourceFenceCurrent(fence) || materializedSourceCurrent(snapshot, old, e.catalog.state, e.storage.Relations[current.Address()]) {
		t.Fatal("plain rewrite bridged active source snapshot")
	}
	e.mu.Unlock()
	// Snapshot vectors remain the old immutable data, not the newly installed bag.
	if len(snapshot.Committed[old.Address()]) != 1 {
		t.Fatal("source snapshot was recaptured")
	}
}
