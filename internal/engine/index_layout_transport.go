package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// evolveIndexRevision records this only for its explicit slot-only path, with
// unchanged referenced Usage. Pointer equality of remapped ASTs is not a proof.
type indexLayoutLineage struct {
	source, target             *catalog.Relation
	sourceParent, targetParent *catalog.Relation
	previous                   *indexLayoutLineage
}

func indexLayoutInputsUnchanged(index, oldParent, parent *catalog.Relation) bool {
	if index == nil || oldParent == nil || parent == nil || oldParent.Address() != parent.Address() {
		return false
	}
	sameAttribute := func(attribute int32) bool {
		oldSlot, newSlot := slices.Index(oldParent.Attributes, attribute), slices.Index(parent.Attributes, attribute)
		return oldSlot >= 0 && newSlot >= 0 && oldParent.Columns[oldSlot].Usage == parent.Columns[newSlot].Usage
	}
	for _, key := range index.SQLIndex.Keys {
		if key.Derived == nil {
			if !sameAttribute(key.Attribute) {
				return false
			}
			continue
		}
		for _, dependency := range key.Derived.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	for _, attribute := range index.SQLIndex.Include {
		if !sameAttribute(attribute) {
			return false
		}
	}
	if predicate := index.SQLIndex.Predicate; predicate != nil {
		for _, dependency := range predicate.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 && !sameAttribute(a.SubID) {
				return false
			}
		}
	}
	return true
}

// Only referenced dense slots affect index extraction authority. Dropping an
// unrelated tail or attaching metadata does not replace an immutable root.
func indexLayoutSlotsUnchanged(index, oldParent, parent *catalog.Relation) bool {
	for oldSlot, attribute := range oldParent.Attributes {
		if indexAttributeAffected(index.SQLIndex, attribute) && slices.Index(parent.Attributes, attribute) != oldSlot {
			return false
		}
	}
	return true
}

// A layout projection preserves surviving cells and exact row/version order.
// Both baseline entries and private row outputs therefore travel without SQL.
func transportIndexLayouts(evaluation *exec.Context, stage *CatalogStage, changes *catalogChanges, parent *catalog.Relation, sourceStore, targetStore *storage.Relation, sourceWrites, writes []storage.RowVersion) ([]storage.RowVersion, bool, error) {
	if len(stage.indexesOrZero().layouts) == 0 {
		return writes, false, nil
	}
	type capture struct {
		lineage indexLayoutLineage
		root    *storage.IndexRoot
	}
	var captures []capture
	e := stage.owner
	e.mu.Lock()
	if e.catalog.state.Epoch() != stage.Base.Epoch() {
		e.mu.Unlock()
		return nil, false, ids.ErrCatalogReplaced
	}
	for address, lineage := range stage.indexesOrZero().layouts {
		if lineage.source == nil || lineage.target == nil || lineage.sourceParent == nil || lineage.targetParent == nil {
			e.mu.Unlock()
			return nil, false, ids.ErrCatalogReplaced
		}
		if lineage.targetParent.Address() != parent.Address() {
			continue
		}
		if lineage.target.SQLIndex.Parent != parent.Address() || !indexLayoutInputsUnchanged(lineage.source, lineage.sourceParent, lineage.targetParent) || !indexLayoutInputsUnchanged(lineage.target, lineage.targetParent, parent) {
			e.mu.Unlock()
			return nil, false, ids.ErrCatalogReplaced
		}
		target, ok := evaluation.Catalog.Relation(address)
		if !ok || !sameIndexKeySemantics(target, lineage.target) {
			e.mu.Unlock()
			return nil, false, ids.ErrCatalogReplaced
		}
		root := e.storage.IndexRoots[address]
		if private := journalIndexesOrZero(changes).uniqueRoots[address]; private != nil {
			candidate, pinned := journalIndexesOrZero(changes).candidates[address]
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
				return nil, false, ids.ErrCatalogReplaced
			}
			root = private
		} else {
			source, ok := e.catalog.state.Relation(address)
			current := e.storage.Relations[parent.Address()]
			for edge := &lineage; edge != nil; edge = edge.previous {
				if ok && sameIndexKeySemantics(source, edge.source) {
					lineage.source, lineage.sourceParent = edge.source, edge.sourceParent
					break
				}
			}
			if !ok || !sameIndexKeySemantics(source, lineage.source) || sourceStore == nil || current != sourceStore.IndexIdentity() || current.Generation != sourceStore.Generation || current.Ends != sourceStore.Ends || len(current.Versions) != len(sourceStore.Versions) {
				e.mu.Unlock()
				return nil, false, ids.ErrCatalogReplaced
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
	if sourceStore == nil || targetStore == nil || len(sourceStore.Versions) != len(targetStore.Versions) {
		return nil, false, ids.ErrCatalogReplaced
	}
	for i, row := range sourceStore.Versions {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		next := targetStore.Versions[i]
		if row.Row != next.Row || row.Version != next.Version {
			return nil, false, ids.ErrCatalogReplaced
		}
	}
	for _, captured := range captures {
		lineage, root := captured.lineage, captured.root
		if sourceStore == targetStore && sameIndexKeySemantics(lineage.source, lineage.target) {
			continue
		}
		if root.Hash != nil && sameIndexKeySemantics(lineage.source, lineage.target) && root.Hash.Storage == targetStore.IndexIdentity() && root.Hash.Generation == targetStore.Generation && root.Hash.Ends == targetStore.Ends && root.Hash.Prefix == len(targetStore.Versions) {
			retainIndexBuild(changes, lineage.target, targetStore, root)
			continue
		}
		if root.Hash != nil {
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if err := evaluation.Context.Err(); err != nil {
						return nil, false, err
					}
					if entry.Position < 0 || entry.Position >= len(sourceStore.Versions) {
						return nil, false, ids.ErrCatalogReplaced
					}
					row := sourceStore.Versions[entry.Position]
					if row.Row != entry.Row || row.Version != entry.Version {
						return nil, false, ids.ErrCatalogReplaced
					}
				}
			}
			binding, err := bindHashIndex(lineage.targetParent, lineage.target.SQLIndex)
			if err != nil {
				return nil, false, err
			}
			next := *root.Hash
			next.Binding, next.Storage, next.Generation = binding, targetStore.IndexIdentity(), targetStore.Generation
			next.Prefix, next.Ends = len(targetStore.Versions), targetStore.Ends
			if err := next.CaptureRowEntries(evaluation.Context, lineage.target.SQLIndex); err != nil {
				return nil, false, err
			}
			root = &storage.IndexRoot{Hash: &next}
		} else {
			for _, entry := range root.Entries {
				if err := evaluation.Context.Err(); err != nil {
					return nil, false, err
				}
				if entry.Position < 0 || entry.Position >= len(sourceStore.Versions) {
					return nil, false, ids.ErrCatalogReplaced
				}
				row := sourceStore.Versions[entry.Position]
				if row.Row != entry.Row || row.Version != entry.Version {
					return nil, false, ids.ErrCatalogReplaced
				}
			}
			if !indexLayoutSlotsUnchanged(lineage.source, lineage.sourceParent, lineage.targetParent) {
				root = newLogicalIndexRoot(lineage.target.SQLIndex, root.Entries)
			}
		}
		retainIndexBuild(changes, lineage.target, targetStore, root)
	}
	if len(writes) == 0 {
		return writes, false, nil
	}
	pending := false
	for _, captured := range captures {
		pending = pending || captured.lineage.target.SQLIndex.HasRuntimeDomainConversion()
	}
	for _, row := range sourceWrites {
		pending = pending || len(row.IndexOutputs) != 0
	}
	if !pending {
		return writes, false, nil
	}
	positions := make(map[indexRowIdentity]int, len(sourceWrites))
	for i, row := range sourceWrites {
		positions[indexRowIdentity{row: row.Row, version: row.Version}] = i
	}
	copied := false
	for i, row := range writes {
		if err := evaluation.Context.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[indexRowIdentity{row: row.Row, version: row.Version}]
		if !ok {
			return nil, false, ids.ErrCatalogReplaced
		}
		var outputs map[ids.ObjectAddress]storage.IndexOutput
		for _, captured := range captures {
			lineage := captured.lineage
			output, ok := sourceWrites[position].IndexOutputs[lineage.source.Address()]
			if !ok && !lineage.target.SQLIndex.HasRuntimeDomainConversion() {
				continue
			}
			if !ok || (!indexOutputMatches(output, lineage.source) && !indexOutputMatches(output, lineage.target)) {
				return nil, false, fmt.Errorf("stuntgres: layout projection is missing produced private index data")
			}
			if outputs == nil {
				outputs = maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
			}
			output.Index = lineage.target
			outputs[lineage.target.Address()] = output
		}
		if outputs != nil {
			if !copied {
				writes = slices.Clone(writes)
				copied = true
			}
			writes[i].IndexOutputs = outputs
		}
	}
	return writes, copied, nil
}

// Preserve exact committed descriptor predecessors while empty roots are built
// unlocked; the normal catalog preflight owns the publication veto.
func (e *Engine) fenceEmptyIndexStorageLocked(snapshot *catalog.State, changes *catalogChanges, address ids.ObjectAddress) {
	for _, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address {
			continue
		}
		if _, captured := changes.originals[index.Address()]; !captured {
			changes.originals[index.Address()] = e.catalog.state.Get(index.Address())
		}
	}
}

// A real whole-storage deletion has no SQL index inputs. Rebind its empty
// physical artifacts to the new incarnation rather than retaining old pins or
// evaluating declarations during publication.
func transportEmptyIndexStorage(ctx context.Context, snapshot *catalog.State, changes *catalogChanges, stage *CatalogStage, address ids.ObjectAddress, store *storage.Relation) error {
	parent, ok := snapshot.Relation(address)
	if !ok || store == nil || len(store.Versions) != 0 {
		return ids.ErrCatalogReplaced
	}
	for _, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !index.SQLIndex.Ready || !index.SQLIndex.Live || parent.RelationKind == catalog.PartitionedRelation {
			indexJournalOf(changes.indexesSlot).forget(index.Address())
			continue
		}
		var root *storage.IndexRoot
		if index.SQLIndex.Method == catalog.HashMethod {
			binding, err := bindHashIndex(parent, index.SQLIndex)
			if err != nil {
				return err
			}
			root = &storage.IndexRoot{Hash: &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: binding, Storage: store.IndexIdentity(), Generation: store.Generation, Ends: store.Ends}}
		} else {
			if err := bindIndexProvider(parent, index.SQLIndex); err != nil {
				return err
			}
			root = newLogicalIndexRoot(index.SQLIndex, nil)
		}
		retainIndexBuild(changes, index, store, root)
		if stage != nil {
			if stage.indexesOrZero().candidates == nil {
				stage.indexes().candidates = make(map[ids.ObjectAddress]indexRootCandidate)
			}
			stage.indexes().candidates[index.Address()] = journalIndexesOrZero(changes).candidates[index.Address()]
		}
	}
	return ctx.Err()
}
