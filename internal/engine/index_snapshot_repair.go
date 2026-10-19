package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func indexHasTableOID(index *catalog.IndexDefinition) bool {
	if predicate := index.Predicate; predicate != nil && (generationHasTableOID(predicate.Definition) || generationHasTableOID(predicate.Execution)) {
		return true
	}
	for _, key := range index.Keys {
		if key.Derived != nil && (generationHasTableOID(key.Derived.Definition) || generationHasTableOID(key.Derived.Execution)) {
			return true
		}
	}
	return false
}

func (snapshot *indexSnapshot) needsOwnerRepair() bool {
	if snapshot == nil {
		return false
	}
	for _, source := range snapshot.sources {
		if snapshot.destination(source.parent.Address()) != source.parent.Address() && indexHasTableOID(source.index.SQLIndex) {
			return true
		}
	}
	return false
}

// Track only generated cells that can invalidate a captured index. False means
// observed unchanged so far, not a missing dependency or a cached SQL result.
func (snapshot *indexSnapshot) trackedGenerations(next *catalog.State) map[ids.ObjectAddress]map[int32]bool {
	if snapshot == nil {
		return nil
	}
	var changed map[ids.ObjectAddress]map[int32]bool
	for _, source := range snapshot.sources {
		address := snapshot.destination(source.parent.Address())
		if address == source.parent.Address() {
			continue
		}
		parent, ok := next.Relation(address)
		if !ok {
			continue
		}
		for attribute, generation := range parent.ColumnMeta.Generations {
			if generation.Kind != catalog.GenerationStored || !generationHasTableOID(generation.Planned) || !indexAttributeAffected(source.index.SQLIndex, attribute) {
				continue
			}
			if changed == nil {
				changed = make(map[ids.ObjectAddress]map[int32]bool)
			}
			if changed[address] == nil {
				changed[address] = make(map[int32]bool)
			}
			changed[address][attribute] = false
		}
	}
	return changed
}

// This is part of the existing admitted restore-repair statement, before its
// Finish. Only genuinely changed inputs or owner-dependent expressions produce
// new entries; all other snapshots retain their original produced data.
func (snapshot *indexSnapshot) repair(ctx context.Context, evaluation *EvalContext, storage map[ids.ObjectAddress]*relationStorage, changed map[ids.ObjectAddress]map[int32]bool, moved map[ids.ObjectAddress]bool, auth AuthorizationView) error {
	if snapshot == nil {
		return nil
	}
	var repairedStores map[ids.ObjectAddress]relationStorage
	for address, source := range snapshot.sources {
		parentAddress := snapshot.destination(source.parent.Address())
		affected := moved[parentAddress] || parentAddress != source.parent.Address() && indexHasTableOID(source.index.SQLIndex)
		for attribute, different := range changed[parentAddress] {
			affected = affected || different && indexAttributeAffected(source.index.SQLIndex, attribute)
		}
		if !affected {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		index, ok := evaluation.catalog.Relation(snapshot.destination(address))
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			return ids.ErrCatalogReplaced
		}
		parent, ok := evaluation.catalog.Relation(parentAddress)
		if !ok || parent.Partition != nil || index.SQLIndex.Parent != parentAddress {
			return ids.ErrCatalogReplaced
		}
		store := storage[parentAddress]
		if store == nil {
			return ids.ErrCatalogReplaced
		}
		root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
		if err != nil {
			return err
		}
		schema := evaluation.catalog.Get(parent.Namespace()).Name()
		if err := validateUniqueRoot(ctx, parent, index, root, schema, evaluation.Output, &auth, true); err != nil {
			return err
		}
		// Transport needs pre-remint row/version identities. Candidate storage
		// is reminted in place afterward; capture one detached vector per table
		// for all of its repaired indexes, borrowing immutable scalar values.
		captured, exists := repairedStores[parentAddress]
		if !exists {
			captured = *store
			captured.Versions = slices.Clone(store.Versions)
			if repairedStores == nil {
				repairedStores = make(map[ids.ObjectAddress]relationStorage)
			}
			repairedStores[parentAddress] = captured
		}
		source.root, source.store = root, captured
		snapshot.sources[address] = source
	}
	return ctx.Err()
}
