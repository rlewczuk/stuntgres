package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/ids"
)

// evolveIndexRevision records this only for its explicit slot-only path, with
// unchanged referenced Usage. Pointer equality of remapped ASTs is not a proof.
type indexLayoutLineage struct {
	source, target             *relationDescriptor
	sourceParent, targetParent *relationDescriptor
	previous                   *indexLayoutLineage
}

func indexLayoutInputsUnchanged(index, oldParent, parent *relationDescriptor) bool {
	if index == nil || oldParent == nil || parent == nil || oldParent.address != parent.address {
		return false
	}
	sameAttribute := func(attribute int32) bool {
		oldSlot, newSlot := slices.Index(oldParent.attributes, attribute), slices.Index(parent.attributes, attribute)
		return oldSlot >= 0 && newSlot >= 0 && oldParent.columns[oldSlot].Usage == parent.columns[newSlot].Usage
	}
	for _, key := range index.sqlIndex.keys {
		if key.derived == nil {
			if !sameAttribute(key.attribute) {
				return false
			}
			continue
		}
		for _, dependency := range key.derived.dependencies {
			a := dependency.Address
			if a.ClassID == parent.address.ClassID && a.ObjectID == parent.address.ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	for _, attribute := range index.sqlIndex.include {
		if !sameAttribute(attribute) {
			return false
		}
	}
	if predicate := index.sqlIndex.predicate; predicate != nil {
		for _, dependency := range predicate.dependencies {
			a := dependency.Address
			if a.ClassID == parent.address.ClassID && a.ObjectID == parent.address.ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	return true
}

// Only referenced dense slots affect index extraction authority. Dropping an
// unrelated tail or attaching metadata does not replace an immutable root.
func indexLayoutSlotsUnchanged(index, oldParent, parent *relationDescriptor) bool {
	for oldSlot, attribute := range oldParent.attributes {
		if indexAttributeAffected(index.sqlIndex, attribute) && slices.Index(parent.attributes, attribute) != oldSlot {
			return false
		}
	}
	return true
}

// A layout projection preserves surviving cells and exact row/version order.
// Both baseline entries and private row outputs therefore travel without SQL.
func transportIndexLayouts(evaluation *EvalContext, stage *CatalogStage, changes *catalogChanges, parent *relationDescriptor, sourceStore, targetStore *relationStorage, sourceWrites, writes []rowVersion) ([]rowVersion, bool, error) {
	if len(stage.indexLayouts) == 0 {
		return writes, false, nil
	}
	type capture struct {
		lineage indexLayoutLineage
		root    *logicalIndexRoot
	}
	var captures []capture
	e := stage.owner
	e.mu.Lock()
	if e.catalog.state.epoch != stage.base.Epoch() {
		e.mu.Unlock()
		return nil, false, ErrCatalogReplaced
	}
	for address, lineage := range stage.indexLayouts {
		if lineage.source == nil || lineage.target == nil || lineage.sourceParent == nil || lineage.targetParent == nil {
			e.mu.Unlock()
			return nil, false, ErrCatalogReplaced
		}
		if lineage.targetParent.address != parent.address {
			continue
		}
		if lineage.target.sqlIndex.parent != parent.address || !indexLayoutInputsUnchanged(lineage.source, lineage.sourceParent, lineage.targetParent) || !indexLayoutInputsUnchanged(lineage.target, lineage.targetParent, parent) {
			e.mu.Unlock()
			return nil, false, ErrCatalogReplaced
		}
		target, ok := evaluation.catalog.Relation(address)
		if !ok || !sameIndexKeySemantics(target, lineage.target) {
			e.mu.Unlock()
			return nil, false, ErrCatalogReplaced
		}
		root := e.indexRoots[address]
		if private := changes.uniqueRoots[address]; private != nil {
			candidate, pinned := changes.indexCandidates[address]
			if pinned {
				for edge := &lineage; edge != nil; edge = edge.previous {
					if candidate.matchesIndex(edge.source, sourceStore) {
						lineage.source, lineage.sourceParent = edge.source, edge.sourceParent
						break
					}
				}
			}
			if !pinned || candidate.root != private || (!candidate.matchesIndex(lineage.source, sourceStore) && !candidate.matchesIndex(lineage.target, targetStore)) {
				e.mu.Unlock()
				return nil, false, ErrCatalogReplaced
			}
			root = private
		} else {
			source, ok := e.catalog.state.Relation(address)
			current := e.storage[parent.address]
			for edge := &lineage; edge != nil; edge = edge.previous {
				if ok && sameIndexKeySemantics(source, edge.source) {
					lineage.source, lineage.sourceParent = edge.source, edge.sourceParent
					break
				}
			}
			if !ok || !sameIndexKeySemantics(source, lineage.source) || sourceStore == nil || current != indexStorageIdentity(sourceStore) || current.generation != sourceStore.generation || current.ends != sourceStore.ends || len(current.versions) != len(sourceStore.versions) {
				e.mu.Unlock()
				return nil, false, ErrCatalogReplaced
			}
		}
		if root == nil {
			e.mu.Unlock()
			return nil, false, fmt.Errorf("stuntgres: layout projection is missing its source index root")
		}
		captures = append(captures, capture{lineage: lineage, root: root})
	}
	e.mu.Unlock()
	if len(captures) == 0 {
		return writes, false, nil
	}
	if sourceStore == nil || targetStore == nil || len(sourceStore.versions) != len(targetStore.versions) {
		return nil, false, ErrCatalogReplaced
	}
	for i, row := range sourceStore.versions {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		next := targetStore.versions[i]
		if row.row != next.row || row.version != next.version {
			return nil, false, ErrCatalogReplaced
		}
	}
	for _, captured := range captures {
		lineage, root := captured.lineage, captured.root
		if sourceStore == targetStore && sameIndexKeySemantics(lineage.source, lineage.target) {
			continue
		}
		if root.hash != nil && sameIndexKeySemantics(lineage.source, lineage.target) && root.hash.storage == indexStorageIdentity(targetStore) && root.hash.generation == targetStore.generation && root.hash.ends == targetStore.ends && root.hash.prefix == len(targetStore.versions) {
			retainIndexBuild(changes, lineage.target, targetStore, root)
			continue
		}
		if root.hash != nil {
			for _, bucket := range root.hash.buckets {
				for _, entry := range bucket {
					if err := evaluation.Context.Err(); err != nil {
						return nil, false, err
					}
					if entry.position < 0 || entry.position >= len(sourceStore.versions) {
						return nil, false, ErrCatalogReplaced
					}
					row := sourceStore.versions[entry.position]
					if row.row != entry.row || row.version != entry.version {
						return nil, false, ErrCatalogReplaced
					}
				}
			}
			binding, err := bindHashIndex(lineage.targetParent, lineage.target.sqlIndex)
			if err != nil {
				return nil, false, err
			}
			next := *root.hash
			next.binding, next.storage, next.generation = binding, indexStorageIdentity(targetStore), targetStore.generation
			next.prefix, next.ends = len(targetStore.versions), targetStore.ends
			if err := next.captureRowEntries(evaluation.Context, lineage.target.sqlIndex); err != nil {
				return nil, false, err
			}
			root = &logicalIndexRoot{hash: &next}
		} else {
			for _, entry := range root.entries {
				if err := evaluation.Context.Err(); err != nil {
					return nil, false, err
				}
				if entry.position < 0 || entry.position >= len(sourceStore.versions) {
					return nil, false, ErrCatalogReplaced
				}
				row := sourceStore.versions[entry.position]
				if row.row != entry.row || row.version != entry.version {
					return nil, false, ErrCatalogReplaced
				}
			}
			if !indexLayoutSlotsUnchanged(lineage.source, lineage.sourceParent, lineage.targetParent) {
				root = newLogicalIndexRoot(lineage.target.sqlIndex, root.entries)
			}
		}
		retainIndexBuild(changes, lineage.target, targetStore, root)
	}
	if len(writes) == 0 {
		return writes, false, nil
	}
	pending := false
	for _, captured := range captures {
		pending = pending || captured.lineage.target.sqlIndex.hasRuntimeDomainConversion()
	}
	for _, row := range sourceWrites {
		pending = pending || len(row.indexOutputs) != 0
	}
	if !pending {
		return writes, false, nil
	}
	positions := make(map[indexRowIdentity]int, len(sourceWrites))
	for i, row := range sourceWrites {
		positions[indexRowIdentity{row: row.row, version: row.version}] = i
	}
	copied := false
	for i, row := range writes {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[indexRowIdentity{row: row.row, version: row.version}]
		if !ok {
			return nil, false, ErrCatalogReplaced
		}
		var outputs map[ids.ObjectAddress]indexRowOutput
		for _, captured := range captures {
			lineage := captured.lineage
			output, ok := sourceWrites[position].indexOutputs[lineage.source.address]
			if !ok && !lineage.target.sqlIndex.hasRuntimeDomainConversion() {
				continue
			}
			if !ok || (!output.matches(lineage.source) && !output.matches(lineage.target)) {
				return nil, false, fmt.Errorf("stuntgres: layout projection is missing produced private index data")
			}
			if outputs == nil {
				outputs = maps.Clone(row.indexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]indexRowOutput)
				}
			}
			output.index = lineage.target
			outputs[lineage.target.address] = output
		}
		if outputs != nil {
			if !copied {
				writes = slices.Clone(writes)
				copied = true
			}
			writes[i].indexOutputs = outputs
		}
	}
	return writes, copied, nil
}

// Preserve exact committed descriptor predecessors while empty roots are built
// unlocked; the normal catalog preflight owns the publication veto.
func (e *Engine) fenceEmptyIndexStorageLocked(catalog *catalogState, changes *catalogChanges, address ids.ObjectAddress) {
	for _, descriptor := range catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != address {
			continue
		}
		if _, captured := changes.originals[index.address]; !captured {
			changes.originals[index.address] = e.catalog.state.Get(index.address)
		}
	}
}

// A real whole-storage deletion has no SQL index inputs. Rebind its empty
// physical artifacts to the new incarnation rather than retaining old pins or
// evaluating declarations during publication.
func transportEmptyIndexStorage(ctx context.Context, catalog *catalogState, changes *catalogChanges, stage *CatalogStage, address ids.ObjectAddress, store *relationStorage) error {
	parent, ok := catalog.Relation(address)
	if !ok || store == nil || len(store.versions) != 0 {
		return ErrCatalogReplaced
	}
	for _, descriptor := range catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != address {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !index.sqlIndex.ready || !index.sqlIndex.live || parent.relationKind == partitionedRelation {
			delete(changes.uniqueRoots, index.address)
			delete(changes.indexCandidates, index.address)
			continue
		}
		var root *logicalIndexRoot
		if index.sqlIndex.method == hashMethod {
			binding, err := bindHashIndex(parent, index.sqlIndex)
			if err != nil {
				return err
			}
			root = &logicalIndexRoot{hash: &hashIndexRoot{buckets: make(map[uint64][]hashIndexEntry), binding: binding, storage: indexStorageIdentity(store), generation: store.generation, ends: store.ends}}
		} else {
			if err := bindIndexProvider(parent, index.sqlIndex); err != nil {
				return err
			}
			root = newLogicalIndexRoot(index.sqlIndex, nil)
		}
		retainIndexBuild(changes, index, store, root)
		if stage != nil {
			if stage.indexCandidates == nil {
				stage.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate)
			}
			stage.indexCandidates[index.address] = changes.indexCandidates[index.address]
		}
	}
	return ctx.Err()
}
