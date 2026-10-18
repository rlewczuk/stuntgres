package engine

import (
	"testing"
)

func TestMaterializedRefreshConcurrentRetainedProvenance(t *testing.T) {
	for _, sequence := range [][]bool{{true, true}, {true, false, true}, {false, true}} {
		e := New()
		d := concurrentUnitRelation(t, e)
		original := e.storage[d.address]
		changes := newCatalogChanges()
		previous := original
		for i, concurrent := range sequence {
			replacement := &relationStorage{generation: previous.generation + 1}
			stage := &CatalogStage{materializedReplacement: &materializedReplacement{original: d, descriptor: d, predecessor: previous, storage: replacement, generation: previous.generation, ends: previous.ends, concurrent: concurrent}}
			e.mu.Lock()
			e.stageCatalogChangesLocked(stage, changes)
			e.mu.Unlock()
			shape := changes.storage[d.address]
			continuous := true
			for _, mode := range sequence[:i+1] {
				continuous = continuous && mode
			}
			if shape.original != original || shape.replacement != replacement || shape.materializedConcurrent != concurrent || shape.materializedContinuous != continuous {
				t.Fatal("lost first predecessor or interrupted provenance", sequence, i, shape)
			}
			if original.materializedLineage != nil || replacement.materializedLineage != nil {
				t.Fatal("private stage stamped committed lineage")
			}
			previous = replacement
		}
	}
	e := New()
	d := concurrentUnitRelation(t, e)
	private := &relationStorage{}
	changes := newCatalogChanges()
	changes.storage[d.address] = catalogStorageChange{replacement: private, relation: d}
	stage := &CatalogStage{materializedReplacement: &materializedReplacement{original: d, descriptor: d, predecessor: private, storage: &relationStorage{}, concurrent: true}}
	e.mu.Lock()
	e.stageCatalogChangesLocked(stage, changes)
	e.mu.Unlock()
	if shape := changes.storage[d.address]; shape.original != nil || shape.materializedContinuous || !shape.materializedConcurrent {
		t.Fatal("private CREATE acquired committed provenance", shape)
	}
}

func TestMaterializedRefreshConcurrentProducerSourceFences(t *testing.T) {
	e := New()
	old := refreshUnitRelation(t, e)
	materializedConcurrentUnitIndex(t, e, old, "unit_u", "i", "")
	old = e.state.descriptors[old.address].(*relationDescriptor)
	old = concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 1), false, true)
	catalog := e.CatalogRead()
	storage := e.storage[old.address]
	snapshot := &visibilityData{catalog: catalog, committed: map[ObjectAddress][]rowVersion{old.address: storage.versions}, singleGeneration: storage}
	fence := materializedSourceFence{ref: e.domain.Ref(catalog.Epoch(), old.address), relation: old, storage: storage}
	current := concurrentUnitReplace(t, e, old, refreshUnitRows(t, old, 2), true, true)
	e.mu.Lock()
	if !materializedSourceCurrent(snapshot, old, e.state, e.storage[old.address]) || !e.materializedSourceFenceCurrent(fence) {
		t.Fatal("compatible publication rejected captured source")
	}
	changes := newCatalogChanges()
	changes.materializedSources = []materializedSourceFence{fence}
	clone := cloneCatalogChanges(changes)
	if err := e.catalogPreflightLocked(clone); err != nil {
		t.Fatal("source-only retained fence rejected", err)
	}
	changes.originals[old.address] = old
	if err := e.catalogPreflightLocked(changes); err == nil {
		t.Fatal("source proof weakened an exact original fence")
	}
	changed := *current
	changed.name = "renamed"
	e.state.descriptors[current.address] = &changed
	if e.materializedSourceFenceCurrent(fence) || materializedSourceCurrent(snapshot, old, e.state, e.storage[old.address]) {
		t.Fatal("source definition retargeted")
	}
	e.state.descriptors[current.address] = current
	e.mu.Unlock()
	current = concurrentUnitReplace(t, e, current, refreshUnitRows(t, current, 3), false, true)
	e.mu.Lock()
	if e.materializedSourceFenceCurrent(fence) || materializedSourceCurrent(snapshot, old, e.state, e.storage[current.address]) {
		t.Fatal("plain rewrite bridged active source snapshot")
	}
	e.mu.Unlock()
	// Snapshot vectors remain the old immutable data, not the newly installed bag.
	if len(snapshot.committed[old.address]) != 1 {
		t.Fatal("source snapshot was recaptured")
	}
}
