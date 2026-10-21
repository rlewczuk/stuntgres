package index

import (
	"context"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

func indexHasTableOID(index *catalog.IndexDefinition) bool {
	if predicate := index.Predicate; predicate != nil && (plan.GenerationHasTableOID(predicate.Definition) || plan.GenerationHasTableOID(predicate.Execution)) {
		return true
	}
	for _, key := range index.Keys {
		if key.Derived != nil && (plan.GenerationHasTableOID(key.Derived.Definition) || plan.GenerationHasTableOID(key.Derived.Execution)) {
			return true
		}
	}
	return false
}

func (snapshot *Snapshot) NeedsOwnerRepair() bool {
	if snapshot == nil {
		return false
	}
	for _, source := range snapshot.Sources {
		if snapshot.Destination(source.Parent.Address()) != source.Parent.Address() && indexHasTableOID(source.Index.SQLIndex) {
			return true
		}
	}
	return false
}

// Track only generated cells that can invalidate a captured index. False means
// observed unchanged so far, not a missing dependency or a cached SQL result.
func (snapshot *Snapshot) TrackedGenerations(next *catalog.State) map[ids.ObjectAddress]map[int32]bool {
	if snapshot == nil {
		return nil
	}
	var changed map[ids.ObjectAddress]map[int32]bool
	for _, source := range snapshot.Sources {
		address := snapshot.Destination(source.Parent.Address())
		if address == source.Parent.Address() {
			continue
		}
		parent, ok := next.Relation(address)
		if !ok {
			continue
		}
		for attribute, generation := range parent.ColumnMeta.Generations {
			if generation.Kind != catalog.GenerationStored || !plan.GenerationHasTableOID(generation.Planned) || !indexAttributeAffected(source.Index.SQLIndex, attribute) {
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
func (snapshot *Snapshot) Repair(ctx context.Context, evaluation *exec.Context, relations map[ids.ObjectAddress]*storage.Relation, changed map[ids.ObjectAddress]map[int32]bool, moved map[ids.ObjectAddress]bool, view access.View, env access.Environment) error {
	if snapshot == nil {
		return nil
	}
	var repairedStores map[ids.ObjectAddress]storage.Relation
	for address, source := range snapshot.Sources {
		parentAddress := snapshot.Destination(source.Parent.Address())
		affected := moved[parentAddress] || parentAddress != source.Parent.Address() && indexHasTableOID(source.Index.SQLIndex)
		for attribute, different := range changed[parentAddress] {
			affected = affected || different && indexAttributeAffected(source.Index.SQLIndex, attribute)
		}
		if !affected {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		index, ok := evaluation.Catalog.Relation(snapshot.Destination(address))
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			return ids.ErrCatalogReplaced
		}
		parent, ok := evaluation.Catalog.Relation(parentAddress)
		if !ok || parent.Partition != nil || index.SQLIndex.Parent != parentAddress {
			return ids.ErrCatalogReplaced
		}
		store := relations[parentAddress]
		if store == nil {
			return ids.ErrCatalogReplaced
		}
		root, err := BuildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
		if err != nil {
			return err
		}
		schema := evaluation.Catalog.Get(parent.Namespace()).Name()
		if err := ValidateUniqueRoot(ctx, parent, index, root, schema, evaluation.Output, &view, env, true); err != nil {
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
				repairedStores = make(map[ids.ObjectAddress]storage.Relation)
			}
			repairedStores[parentAddress] = captured
		}
		source.Root, source.Store = root, captured
		snapshot.Sources[address] = source
	}
	return ctx.Err()
}
