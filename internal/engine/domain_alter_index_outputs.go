package engine

import (
	"context"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func reownDomainIndexOutputs(ctx context.Context, base, next *catalog.State, original, store *relationStorage) (*relationStorage, error) {
	var versions []rowVersion
	for i, row := range original.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(row.IndexOutputs) == 0 {
			continue
		}
		var outputs map[ids.ObjectAddress]storage.IndexOutput
		for address, output := range row.IndexOutputs {
			old, ok := base.Relation(address)
			index, present := next.Relation(address)
			if !ok || !present || !indexOutputMatches(output, old) {
				continue
			}
			parent := next.MustRelation(index.SQLIndex.Parent)
			mapped := output
			mapped.Index = index
			keysChanged := false
			if output.Key.Type() != nil {
				usage, err := indexKeyUsage(parent, index.SQLIndex.Keys[0])
				if err != nil {
					return nil, err
				}
				mapped.Key, err = reownDomainDatum(output.Key, usage.Type, nil, next.ScalarImports(), next.ACLItemImport())
				if err != nil {
					return nil, err
				}
			}
			for key, value := range output.Keys {
				usage, err := indexKeyUsage(parent, index.SQLIndex.Keys[key])
				if err != nil {
					return nil, err
				}
				rebound, err := reownDomainDatum(value, usage.Type, nil, next.ScalarImports(), next.ACLItemImport())
				if err != nil {
					return nil, err
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
			if store.Versions[i].IndexOutputs != nil {
				continue
			}
			outputs = row.IndexOutputs
		}
		if versions == nil {
			versions = slices.Clone(store.Versions)
		}
		versions[i].IndexOutputs = outputs
	}
	if versions == nil {
		return store, nil
	}
	nextStore := *store
	nextStore.Versions = versions
	return &nextStore, nil
}
