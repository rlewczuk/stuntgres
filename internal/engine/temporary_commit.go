package engine

import (
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// Prepare descriptor-owned actions only at an outer successful commit. The
// transaction/savepoint roots remain untouched until the ordinary final install.
func (e *Engine) prepareTemporaryCommit(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *catalogChanges) (map[ids.ObjectAddress][]storage.RowVersion, map[ids.ObjectAddress]*visibility.PrivateDeletes, *catalogChanges, error) {
	e.mu.Lock()
	if ref.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return nil, nil, nil, ids.ErrCatalogReplaced
	}
	base := e.privateCatalogLocked(c)
	if c == nil && op.Session.TemporaryNamespace == (ids.ObjectAddress{}) {
		e.mu.Unlock()
		return writes, deletes, c, nil
	}
	var cleared, dropped []ids.ObjectAddress
	for a, descriptor := range base.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.RelationKind != catalog.TableRelation || table.OnCommit != parser.OnCommitDelete && table.OnCommit != parser.OnCommitDrop {
			continue
		}
		ns := base.MustNamespace(table.Namespace())
		if ns.Temporary != op.Session.SessionToken {
			continue
		}
		if table.OnCommit == parser.OnCommitDrop {
			dropped = append(dropped, a)
			continue
		}
		store := e.privateStorageLocked(c, a)
		if store != nil && (len(store.Versions) != 0 || len(writes[a]) != 0 || deletes[a] != nil) {
			cleared = append(cleared, a)
		}
	}
	if len(cleared) == 0 && len(dropped) == 0 {
		e.mu.Unlock()
		return writes, deletes, c, nil
	}
	next := c.Clone()
	nextWrites, nextDeletes := maps.Clone(writes), maps.Clone(deletes)
	for _, a := range cleared {
		shape, exists := next.Storage[a]
		if !exists {
			store := e.storage.Relations[a]
			shape = catalogStorageChange{Original: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation}
		}
		shape.Replacement = &storage.Relation{}
		shape.Truncated = false
		shape.Relation = base.MustRelation(a)
		next.Storage[a] = shape
		e.fenceEmptyIndexStorageLocked(base.State(), next, a)
		delete(nextWrites, a)
		delete(nextDeletes, a)
	}
	e.mu.Unlock()
	for _, address := range cleared {
		if err := transportEmptyIndexStorage(op.Context(), base.State(), next, nil, address, next.Storage[address].Replacement); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(dropped) != 0 {
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		input, output := sessionPresentation(op.Session)
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: input, Output: output, Catalog: base}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		project := func(old, replacement *catalog.Relation) error {
			owner := op.Session.Transactions
			snapshot, exists := owner.roots[old.Address()]
			rows, deleted, _, err := stagePrivateColumnShape(&evaluation, stage, next, old, replacement, nextWrites[old.Address()], nextDeletes[old.Address()], snapshot, exists, stableIsolation(owner.isolation))
			if err != nil {
				return err
			}
			if rows != nil || nextWrites != nil {
				if nextWrites == nil {
					nextWrites = make(map[ids.ObjectAddress][]storage.RowVersion)
				}
				nextWrites[old.Address()] = rows
			}
			if deleted != nil || nextDeletes != nil {
				if nextDeletes == nil {
					nextDeletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
				}
				nextDeletes[old.Address()] = deleted
			}
			return nil
		}
		for _, a := range dropped {
			if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), a)); err != nil {
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
			for a := range stage.Descriptors() {
				original, observed := next.Originals[a]
				if !observed {
					original = base.Get(a)
				}
				if e.catalog.state.Get(a) != original {
					err = catalog.Conflict{}
					break
				}
			}
			for a := range stage.Removals() {
				original, observed := next.Originals[a]
				if !observed {
					original = base.Get(a)
				}
				if e.catalog.state.Get(a) != original {
					err = catalog.Conflict{}
					break
				}
			}
		}
		if err == nil {
			e.stageCatalogChangesLocked(stage, next)
			for a := range stage.Removals() {
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
