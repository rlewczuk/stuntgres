package engine

import (
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Prepare descriptor-owned actions only at an outer successful commit. The
// transaction/savepoint roots remain untouched until the ordinary final install.
func (e *Engine) prepareTemporaryCommit(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]rowVersion, deletes map[ids.ObjectAddress]*privateDeletionRoot, c *catalogChanges) (map[ids.ObjectAddress][]rowVersion, map[ids.ObjectAddress]*privateDeletionRoot, *catalogChanges, error) {
	e.mu.Lock()
	if ref.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		return nil, nil, nil, ids.ErrCatalogReplaced
	}
	base := e.privateCatalogLocked(c)
	if c == nil && op.session.temporaryNamespace == (ids.ObjectAddress{}) {
		e.mu.Unlock()
		return writes, deletes, c, nil
	}
	var cleared, dropped []ids.ObjectAddress
	for a, descriptor := range base.Descriptors() {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.relationKind != tableRelation || table.onCommit != parser.OnCommitDelete && table.onCommit != parser.OnCommitDrop {
			continue
		}
		ns := base.MustNamespace(table.namespace)
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
		shape.relation = base.MustRelation(a)
		next.storage[a] = shape
		e.fenceEmptyIndexStorageLocked(base.state, next, a)
		delete(nextWrites, a)
		delete(nextDeletes, a)
	}
	e.mu.Unlock()
	for _, address := range cleared {
		if err := transportEmptyIndexStorage(op.Context(), base.state, next, nil, address, next.storage[address].replacement); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(dropped) != 0 {
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		input, output := sessionPresentation(op.session)
		evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: input, Output: output, catalog: base}
		project := func(old, replacement *relationDescriptor) error {
			owner := op.session.transactions
			snapshot, exists := owner.roots[old.address]
			rows, deleted, _, err := stagePrivateColumnShape(&evaluation, stage, next, old, replacement, nextWrites[old.address], nextDeletes[old.address], snapshot, exists, stableIsolation(owner.isolation))
			if err != nil {
				return err
			}
			if rows != nil || nextWrites != nil {
				if nextWrites == nil {
					nextWrites = make(map[ids.ObjectAddress][]rowVersion)
				}
				nextWrites[old.address] = rows
			}
			if deleted != nil || nextDeletes != nil {
				if nextDeletes == nil {
					nextDeletes = make(map[ids.ObjectAddress]*privateDeletionRoot)
				}
				nextDeletes[old.address] = deleted
			}
			return nil
		}
		for _, a := range dropped {
			if err := stage.Remove(e.catalog.domain.Ref(base.Epoch(), a)); err != nil {
				return nil, nil, nil, err
			}
		}
		if err := stageExpressionDependencies(op.Context(), stage, dropped, parser.DropCascade, nil, next, project, ids.ObjectAddress{}); err != nil {
			return nil, nil, nil, err
		}
		if err := stageOwnedExpressionDependencies(op.Context(), stage, dropped, parser.DropCascade, nil, next, project); err != nil {
			return nil, nil, nil, err
		}
		e.mu.Lock()
		err := e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil)
		if err == nil {
			for a := range stage.descriptors {
				original, observed := next.originals[a]
				if !observed {
					original = base.Get(a)
				}
				if e.catalog.state.Get(a) != original {
					err = CatalogConflict{}
					break
				}
			}
			for a := range stage.removals {
				original, observed := next.originals[a]
				if !observed {
					original = base.Get(a)
				}
				if e.catalog.state.Get(a) != original {
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
