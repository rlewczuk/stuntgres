package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

// Return a detached descriptor when a dependent attribute TYPE or schema/owner
// changes. TYPE reconstructs the whole definition using OLD type defaults,
// matching PostgreSQL's captured pg_get_indexdef rather than original spelling.
func evolveIndex(index, oldParent, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if parent == nil {
		return nil, nil
	}
	if index.sqlIndex.unique != nil {
		found := false
		for _, c := range parent.uniqueConstraints {
			if c.Index == index.address && c.OID == index.sqlIndex.unique.OID {
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}
	definition := index.sqlIndex
	reconstruct := false
	dependentAttribute := func(attribute int32) bool {
		if !parent.hasAttribute(attribute) {
			return false
		}
		for _, transformation := range transformations {
			if transformation.attribute == attribute {
				reconstruct = true
				break
			}
		}
		return true
	}
	for i, key := range index.sqlIndex.keys {
		if key.derived == nil {
			if !dependentAttribute(key.attribute) {
				return nil, nil
			}
			continue
		}
		var slots map[int32]int
		for _, dependency := range key.derived.dependencies {
			a := dependency.Address
			if a.ClassID == parent.address.ClassID && a.ObjectID == parent.address.ObjectID && a.SubID != 0 {
				if !dependentAttribute(a.SubID) {
					return nil, nil
				}
				if !slices.Equal(key.derived.attributes, parent.attributes) && slots == nil {
					slots = make(map[int32]int, len(parent.attributes))
					for slot, id := range parent.attributes {
						slots[id] = slot
					}
				}
			}
		}
		if slots == nil {
			continue
		}
		remap := func(ref ObjectRef) ObjectRef { return ref }
		node, err := remapBoundExpression(key.derived.definition, remap, slots)
		if err != nil {
			return nil, err
		}
		execution, err := remapBoundExpression(key.derived.execution, remap, slots)
		if err != nil {
			return nil, err
		}
		if definition == index.sqlIndex {
			copy := *definition
			copy.keys = append([]indexKey(nil), copy.keys...)
			definition = &copy
		}
		derived := *key.derived
		derived.definition, derived.execution = node, execution
		derived.attributes = parent.attributes
		definition.keys[i].derived = &derived
	}
	for _, attribute := range definition.include {
		if !dependentAttribute(attribute) {
			return nil, nil
		}
	}
	if reconstruct {
		if definition == index.sqlIndex {
			next := *definition
			next.keys = append([]indexKey(nil), definition.keys...)
			definition = &next
		}
		for i, key := range definition.keys {
			var oldUsage, newUsage sqltype.Usage
			if key.derived != nil {
				oldUsage, newUsage = key.derived.definition.ResultUsage(), key.derived.execution.ResultUsage()
			} else {
				oldUsage = oldParent.columns[slices.Index(oldParent.attributes, key.attribute)].Usage
				newUsage = parent.columns[slices.Index(parent.attributes, key.attribute)].Usage
			}
			definition.keys[i] = reconstructIndexKey(key, oldUsage, newUsage)
		}
	}
	if _, err := bindIndexOrdering(parent, definition); err != nil {
		return nil, err
	}
	changed := definition != index.sqlIndex || index.namespace != parent.namespace || index.owner != parent.owner
	for i, key := range definition.keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		changed = changed || index.columns[i].Usage != usage
	}
	if !changed {
		return index, nil
	}
	clone := *index
	clone.namespace, clone.owner = parent.namespace, parent.owner
	clone.sqlIndex = definition
	clone.columns = append([]ColumnDescriptor(nil), index.columns...)
	for i, key := range definition.keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		clone.columns[i].Usage = usage
	}
	for i, attribute := range definition.include {
		slot := slices.Index(parent.attributes, attribute)
		clone.columns[len(definition.keys)+i].Usage = parent.columns[slot].Usage
	}
	clone.revision++
	if clone.revision == 0 {
		return nil, fmt.Errorf("stuntgres: index revision exhausted")
	}
	return &clone, nil
}
func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage, typeParent ObjectAddress, transformations []columnTransformation) error {
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
		oldParent := stage.base.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		var dependentTypes []columnTransformation
		if index.sqlIndex.parent == typeParent {
			dependentTypes = transformations
		}
		next, err := evolveIndex(index, oldParent, relation, dependentTypes)
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
func indexStoresUnchanged(storage, captured map[ObjectAddress]*relationStorage, current, next *catalogState, prepared, previous map[ObjectAddress]*logicalIndexRoot) bool {
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
		if current.descriptors[a] == nil {
			if live != nil || old != nil {
				return false
			}
			continue
		}
		if live == nil || old == nil || live.generation != old.generation || live.ends != old.ends || len(live.versions) != len(old.versions) {
			return false
		}
	}
	return true
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func (e *Engine) prepareIndexCatalog(ctx context.Context, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuild map[ObjectAddress]bool) (map[ObjectAddress]*logicalIndexRoot, error) {
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
			if err := validateIndexDefinition(CatalogRead{owner: e, state: next}, index); err != nil {
				return nil, err
			}
			parent := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			var err error
			root, err = buildIndexRoot(ctx, parent, index.sqlIndex, storage[parent.address])
			if err != nil {
				return nil, err
			}
			if index.sqlIndex.unique != nil {
				namespace := next.descriptors[parent.namespace].(*namespaceDescriptor)
				if err := validateUniqueRoot(ctx, parent, index.sqlIndex, root, namespace.name, true); err != nil {
					return nil, err
				}
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

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(catalog *catalogChanges, deletes map[ObjectAddress]*privateDeletionRoot) error {
	if len(s.stage.rows) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(catalog)
	e.mu.Unlock()
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		rows := s.stage.rows[index.sqlIndex.parent]
		if len(rows) == 0 {
			continue
		}
		for _, key := range index.sqlIndex.keys {
			if key.derived == nil {
				continue
			}
			for _, row := range rows {
				if !liveVersion(row, CommitSeq(^uint64(0)), nil, deletes[index.sqlIndex.parent]) {
					continue
				}
				if err := evaluation.Context.Err(); err != nil {
					return err
				}
				evaluation.Row = row.values
				if _, err := key.derived.execution.Eval(&evaluation); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
