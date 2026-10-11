package engine

import "slices"

type catalogOverlayIndex struct {
	index, parent *relationDescriptor
	store         relationStorage
}

// Ordinary key providers run only on detached storage captures, never under
// publication locks. The committed baseline remains distinct from own rows;
// reconstructing the overlay validates CREATE/REINDEX at this statement, with
// build diagnostics, rather than incorrectly delaying errors until COMMIT.
func (e *Engine) prepareCatalogOverlayIndexes(scope *StatementScope, stage *CatalogStage, c *catalogChanges) error {
	e.mu.Lock()
	if scope.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	auth.catalog = view
	var captures []catalogOverlayIndex
	for a, d := range view.state.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		parent, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		old, existed := stage.base.state.descriptors[parent.address].(*relationDescriptor)
		shape, replaced := c.storage[parent.address]
		storageChanged := replaced && shape.replacement != e.privateStorageLocked(scope.owner.catalog, parent.address)
		if !storageChanged && stage.descriptors[a] == nil && stage.indexRebuilds[a] == nil && (!existed || slices.Equal(old.attributes, parent.attributes)) {
			continue
		}
		if !index.sqlIndex.ready {
			delete(c.uniqueRoots, a)
			continue
		}
		// Virtual indexes own ancestry/validity metadata, never heap captures or
		// installation roots. Their exact descriptor fences were captured above.
		if parent.partition != nil {
			continue
		}
		// Unchanged key semantics permit root reuse only on the same storage
		// incarnation; a replacement heap needs its own physical baseline.
		if previous, ok := stage.base.state.descriptors[a].(*relationDescriptor); !storageChanged && ok && previous.sqlIndex != nil && previous.sqlIndex.ready && stage.indexRebuilds[a] == nil && existed && slices.Equal(old.attributes, parent.attributes) && sameIndexKeySemantics(previous, index) {
			continue
		}
		store := e.privateStorageLocked(c, parent.address)
		if store == nil {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		captures = append(captures, catalogOverlayIndex{index: index, parent: parent, store: *store})
		committed := e.storage[parent.address]
		fence := partitionReadFence{address: parent.address, storage: committed}
		if committed != nil {
			fence.length = len(committed.versions)
			fence.ends = committed.ends
			fence.generation = committed.generation
		}
		c.readFences = append(c.readFences, fence)
	}
	e.mu.Unlock()
	for i := range captures {
		capture := &captures[i]
		index, parent := capture.index, capture.parent
		baseline, err := buildIndexRoot(scope.op.Context(), parent, index.sqlIndex, &capture.store)
		if err != nil {
			return err
		}
		writes, deletes := scope.columnProjectionRoots(parent.address)
		if stage.initialTables[parent.address] != nil {
			writes = scope.stage.rows[parent.address]
		}
		publication := rowPublication{address: parent.address, store: &capture.store, root: capture.store.versions, ends: capture.store.ends, writes: writes, deletion: deletes, relation: parent, singleSlot: -1}
		schema := view.state.descriptors[parent.namespace].Name()
		if index.sqlIndex.method == hashMethod {
			full, err := mergeHashIndexRoot(scope.op.Context(), parent, index, baseline, &publication, true, index.sqlIndex.isExclusion())
			if err != nil {
				return err
			}
			if index.sqlIndex.isExclusion() {
				if full == nil {
					full = baseline
				}
				if err = validateExclusionRoot(scope.op.Context(), parent, index, full, schema, scope.output, &auth, true); err != nil {
					return err
				}
			}
		} else {
			full, err := mergeIndexRoot(scope.op.Context(), parent, index, baseline.entries, &publication, true, index.sqlIndex.isExclusion(), true, schema, scope.output, &auth)
			if err != nil {
				return err
			}
			if index.sqlIndex.isExclusion() {
				if full == nil {
					full = baseline
				}
				if err = validateExclusionRoot(scope.op.Context(), parent, index, full, schema, scope.output, &auth, true); err != nil {
					return err
				}
			}
		}
		if c.uniqueRoots == nil {
			c.uniqueRoots = make(map[ObjectAddress]*logicalIndexRoot, len(captures))
		}
		c.uniqueRoots[index.address] = baseline
	}
	return scope.op.Context().Err()
}
