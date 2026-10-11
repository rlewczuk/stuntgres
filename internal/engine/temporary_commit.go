package engine

import (
	"maps"
	"stuntgres/internal/parser"
)

// Prepare descriptor-owned actions only at an outer successful commit. The
// transaction/savepoint roots remain untouched until the ordinary final install.
func (e *Engine) prepareTemporaryCommit(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion, deletes map[ObjectAddress]*privateDeletionRoot, c *catalogChanges) (map[ObjectAddress][]rowVersion, map[ObjectAddress]*privateDeletionRoot, *catalogChanges, error) {
	e.mu.Lock()
	if ref.epoch != e.state.epoch {
		e.mu.Unlock()
		return nil, nil, nil, ErrCatalogReplaced
	}
	base := e.privateCatalogLocked(c)
	if c == nil && op.session.temporaryNamespace == (ObjectAddress{}) {
		e.mu.Unlock()
		return writes, deletes, c, nil
	}
	var cleared, dropped []ObjectAddress
	for a, descriptor := range base.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.relationKind != tableRelation || table.onCommit != parser.OnCommitDelete && table.onCommit != parser.OnCommitDrop {
			continue
		}
		ns := base.state.descriptors[table.namespace].(*namespaceDescriptor)
		if ns.temporary != op.session.token {
			continue
		}
		if table.onCommit == parser.OnCommitDrop {
			dropped = append(dropped, a)
			continue
		}
		store := e.privateStorageLocked(c, a)
		if store != nil && (len(store.versions) != 0 || len(writes[a]) != 0 || deletes[a] != nil) {
			cleared = append(cleared, a)
		}
	}
	if len(cleared) == 0 && len(dropped) == 0 {
		e.mu.Unlock()
		return writes, deletes, c, nil
	}
	next := cloneCatalogChanges(c)
	nextWrites, nextDeletes := maps.Clone(writes), maps.Clone(deletes)
	for _, a := range cleared {
		shape, exists := next.storage[a]
		if !exists {
			store := e.storage[a]
			shape = catalogStorageChange{original: store, root: store.versions, ends: store.ends, generation: store.generation}
		}
		shape.replacement = &relationStorage{}
		shape.truncated = false
		shape.relation = base.state.descriptors[a].(*relationDescriptor)
		next.storage[a] = shape
		delete(nextWrites, a)
		delete(nextDeletes, a)
	}
	e.mu.Unlock()
	if len(dropped) != 0 {
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		project := func(old, replacement *relationDescriptor) error {
			owner := op.session.transactions
			snapshot, exists := owner.roots[old.address]
			rows, deleted, _, err := stagePrivateColumnShape(op.Context(), stage, next, old, replacement, nextWrites[old.address], nextDeletes[old.address], snapshot, exists, stableIsolation(owner.isolation))
			if err != nil {
				return err
			}
			if rows != nil || nextWrites != nil {
				if nextWrites == nil {
					nextWrites = make(map[ObjectAddress][]rowVersion)
				}
				nextWrites[old.address] = rows
			}
			if deleted != nil || nextDeletes != nil {
				if nextDeletes == nil {
					nextDeletes = make(map[ObjectAddress]*privateDeletionRoot)
				}
				nextDeletes[old.address] = deleted
			}
			return nil
		}
		for _, a := range dropped {
			if err := stage.Remove(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: a}); err != nil {
				return nil, nil, nil, err
			}
		}
		if err := stageExpressionDependencies(op.Context(), stage, dropped, parser.DropCascade, nil, next, project); err != nil {
			return nil, nil, nil, err
		}
		if err := stageOwnedExpressionDependencies(op.Context(), stage, dropped, parser.DropCascade, nil, next, project); err != nil {
			return nil, nil, nil, err
		}
		e.mu.Lock()
		err := e.evolveStageIndexesLocked(stage, ObjectAddress{}, nil)
		if err == nil {
			for a := range stage.descriptors {
				original, observed := next.originals[a]
				if !observed {
					original = base.state.descriptors[a]
				}
				if e.state.descriptors[a] != original {
					err = CatalogConflict{}
					break
				}
			}
			for a := range stage.removals {
				original, observed := next.originals[a]
				if !observed {
					original = base.state.descriptors[a]
				}
				if e.state.descriptors[a] != original {
					err = CatalogConflict{}
					break
				}
			}
		}
		if err == nil {
			e.stageCatalogChangesLocked(stage, next)
			for a := range stage.removals {
				delete(nextWrites, a)
				delete(nextDeletes, a)
			}
		}
		e.mu.Unlock()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return nextWrites, nextDeletes, next, nil
}
