package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/ids"
)

func indexHasTableOID(index *indexDefinition) bool {
	if predicate := index.predicate; predicate != nil && (generationHasTableOID(predicate.definition) || generationHasTableOID(predicate.execution)) {
		return true
	}
	for _, key := range index.keys {
		if key.derived != nil && (generationHasTableOID(key.derived.definition) || generationHasTableOID(key.derived.execution)) {
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
		if snapshot.destination(source.parent.address) != source.parent.address && indexHasTableOID(source.index.SQLIndex) {
			return true
		}
	}
	return false
}

// Track only generated cells that can invalidate a captured index. False means
// observed unchanged so far, not a missing dependency or a cached SQL result.
func (snapshot *indexSnapshot) trackedGenerations(next *catalogState) map[ids.ObjectAddress]map[int32]bool {
	if snapshot == nil {
		return nil
	}
	var changed map[ids.ObjectAddress]map[int32]bool
	for _, source := range snapshot.sources {
		address := snapshot.destination(source.parent.address)
		if address == source.parent.address {
			continue
		}
		parent, ok := next.Relation(address)
		if !ok {
			continue
		}
		for attribute, generation := range parent.Generations {
			if generation.kind != generationStored || !generationHasTableOID(generation.planned) || !indexAttributeAffected(source.index.SQLIndex, attribute) {
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
		parentAddress := snapshot.destination(source.parent.address)
		affected := moved[parentAddress] || parentAddress != source.parent.address && indexHasTableOID(source.index.SQLIndex)
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
		if !ok || index.SQLIndex == nil || !index.SQLIndex.ready || !index.SQLIndex.live {
			return ids.ErrCatalogReplaced
		}
		parent, ok := evaluation.catalog.Relation(parentAddress)
		if !ok || parent.Partition != nil || index.SQLIndex.parent != parentAddress {
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
		schema := evaluation.catalog.Get(parent.namespace).Name()
		if err := validateUniqueRoot(ctx, parent, index, root, schema, evaluation.Output, &auth, true); err != nil {
			return err
		}
		// Transport needs pre-remint row/version identities. Candidate storage
		// is reminted in place afterward; capture one detached vector per table
		// for all of its repaired indexes, borrowing immutable scalar values.
		captured, exists := repairedStores[parentAddress]
		if !exists {
			captured = *store
			captured.versions = slices.Clone(store.versions)
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
