package index

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

func ReownDomainIndexOutputs(ctx context.Context, base, next *catalog.State, original, store *storage.Relation) (*storage.Relation, error) {
	var versions []storage.RowVersion
	for i, row := range original.Versions {
		mapped, changed, err := reownDomainIndexOutputVersion(ctx, base, next, row, store.Versions[i])
		if err != nil {
			return nil, err
		}
		prior := row.PrivateHistory
		history, err := storage.MapHeapPredecessors(mapped.PrivateHistory, func(target storage.RowVersion) (storage.RowVersion, bool, error) {
			if prior == nil || prior.Version.Version != target.Version {
				return storage.RowVersion{}, false, fmt.Errorf("stuntgres: changed private predecessor identity during type transport")
			}
			source := prior.Version
			prior = prior.Next
			return reownDomainIndexOutputVersion(ctx, base, next, source, target)
		})
		if err != nil {
			return nil, err
		}
		if changed || history != mapped.PrivateHistory {
			if versions == nil {
				versions = slices.Clone(store.Versions)
			}
			mapped.PrivateHistory = history
			versions[i] = mapped
		}
	}
	if versions == nil {
		return store, nil
	}
	nextStore := *store
	nextStore.Versions = versions
	return &nextStore, nil
}

func reownDomainIndexOutputVersion(ctx context.Context, base, next *catalog.State, row, target storage.RowVersion) (storage.RowVersion, bool, error) {
	if err := ctx.Err(); err != nil {
		return storage.RowVersion{}, false, err
	}
	if len(row.IndexOutputs) == 0 {
		return target, false, nil
	}
	var outputs map[ids.ObjectAddress]storage.IndexOutput
	for address, output := range row.IndexOutputs {
		old, ok := base.Relation(address)
		index, present := next.Relation(address)
		if !ok || !present || !OutputMatches(output, old) {
			continue
		}
		parent := next.MustRelation(index.SQLIndex.Parent)
		mapped := output
		mapped.Index = index
		keysChanged := false
		if output.Key.Type() != nil {
			usage, err := KeyUsage(parent, index.SQLIndex.Keys[0])
			if err != nil {
				return storage.RowVersion{}, false, err
			}
			mapped.Key, err = typedef.ReownDomainDatum(output.Key, usage.Type, nil, next.ScalarImports(), next.ACLItemImport())
			if err != nil {
				return storage.RowVersion{}, false, err
			}
		}
		for key, value := range output.Keys {
			usage, err := KeyUsage(parent, index.SQLIndex.Keys[key])
			if err != nil {
				return storage.RowVersion{}, false, err
			}
			rebound, err := typedef.ReownDomainDatum(value, usage.Type, nil, next.ScalarImports(), next.ACLItemImport())
			if err != nil {
				return storage.RowVersion{}, false, err
			}
			if rebound != value {
				if len(mapped.Keys) != 0 && &mapped.Keys[0] == &output.Keys[0] {
					mapped.Keys = slices.Clone(output.Keys)
				}
				mapped.Keys[key] = rebound
				keysChanged = true
			}
		}
		if mapped.Index == output.Index && mapped.Key == output.Key && !keysChanged {
			continue
		}
		if outputs == nil {
			outputs = maps.Clone(row.IndexOutputs)
		}
		outputs[address] = mapped
	}
	// Retain genuinely stale outputs as stale; never certify/re-execute them.
	if outputs == nil {
		if target.IndexOutputs != nil {
			return target, false, nil
		}
		outputs = row.IndexOutputs
	}
	target.IndexOutputs = outputs
	return target, true, nil
}
