package engine

import (
	"context"
	"fmt"
)

// Return a detached descriptor when an existing parent changes key usages or
// schema/owner. A missing parent/key automatically removes the dependent index.
func evolveIndex(index *relationDescriptor, parent *relationDescriptor) (*relationDescriptor, error) {
	if parent == nil {
		return nil, nil
	}
	for _, key := range index.sqlIndex.keys {
		if !parent.hasAttribute(key.attribute) {
			return nil, nil
		}
	}
	o, err := bindIndexOrdering(parent, index.sqlIndex)
	if err != nil {
		return nil, err
	}
	changed := index.namespace != parent.namespace || index.owner != parent.owner
	for i, slot := range o.slots {
		changed = changed || index.columns[i].Usage != parent.columns[slot].Usage
	}
	if !changed {
		return index, nil
	}
	clone := *index
	clone.namespace, clone.owner = parent.namespace, parent.owner
	clone.columns = append([]ColumnDescriptor(nil), index.columns...)
	for i, slot := range o.slots {
		clone.columns[i].Usage = parent.columns[slot].Usage
	}
	clone.revision++
	if clone.revision == 0 {
		return nil, fmt.Errorf("stuntgres: index revision exhausted")
	}
	return &clone, nil
}
func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage) error {
	for a, d := range stage.base.state.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if _, removed := stage.removals[a]; removed {
			continue
		}
		if _, changed := stage.descriptors[a]; changed {
			continue
		}
		var parent Descriptor
		if _, removed := stage.removals[index.sqlIndex.parent]; removed {
			parent = nil
		} else if replacement, changed := stage.descriptors[index.sqlIndex.parent]; changed {
			parent = replacement
		} else {
			continue
		}
		relation, _ := parent.(*relationDescriptor)
		next, err := evolveIndex(index, relation)
		if err != nil {
			return err
		}
		if next == nil {
			if err := stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: a}, false); err != nil {
				return err
			}
		} else if next != index {
			if err := stage.Replace(next); err != nil {
				return err
			}
		}
	}
	return nil
}

// Captured heads are immutable; the copied scalar generation/end-root tuple
// fences every build against row publication, even when catalog is unchanged.
func captureIndexStores(storage map[ObjectAddress]*relationStorage, catalog *catalogState) map[ObjectAddress]*relationStorage {
	var captured map[ObjectAddress]*relationStorage
	for _, d := range catalog.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if captured == nil {
			captured = make(map[ObjectAddress]*relationStorage)
		}
		a := index.sqlIndex.parent
		if captured[a] != nil {
			continue
		}
		if store := storage[a]; store != nil {
			copy := *store
			captured[a] = &copy
		}
	}
	return captured
}
func indexStoresUnchanged(storage, captured map[ObjectAddress]*relationStorage, next *catalogState, prepared, previous map[ObjectAddress]*logicalIndexRoot) bool {
	if len(prepared) == 0 {
		return true
	}
	for address, d := range next.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || prepared[address] == previous[address] {
			continue
		}
		a := index.sqlIndex.parent
		live, old := storage[a], captured[a]
		if live == nil || old == nil || live.generation != old.generation || live.ends != old.ends || len(live.versions) != len(old.versions) {
			return false
		}
	}
	return true
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func prepareIndexCatalog(ctx context.Context, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuild map[ObjectAddress]bool) (map[ObjectAddress]*logicalIndexRoot, error) {
	var result map[ObjectAddress]*logicalIndexRoot
	for a, d := range next.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if result == nil {
			result = make(map[ObjectAddress]*logicalIndexRoot, len(roots))
		}
		root := roots[a]
		old, _ := current.descriptors[a].(*relationDescriptor)
		if root == nil || old == nil || old.sqlIndex != index.sqlIndex || rebuild[index.sqlIndex.parent] {
			if err := validateIndexDefinition(CatalogRead{state: next}, index); err != nil {
				return nil, err
			}
			parent := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			var err error
			root, err = buildIndexRoot(ctx, parent, index.sqlIndex, storage[parent.address])
			if err != nil {
				return nil, err
			}
		}
		result[a] = root
	}
	return result, nil
}

// Only changed roots require a storage fence. Unchanged siblings may have
// committed while preparation ran; retain their newest immutable roots.
func preserveUnchangedIndexRoots(prepared, previous, live map[ObjectAddress]*logicalIndexRoot) {
	for address, root := range prepared {
		if root == previous[address] {
			prepared[address] = live[address]
		}
	}
}
