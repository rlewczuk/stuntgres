package engine

import (
	"context"
	"maps"
	"slices"
)

func reownDomainIndexOutputs(ctx context.Context, base, next *catalogState, original, store *relationStorage) (*relationStorage, error) {
	var versions []rowVersion
	for i, row := range original.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(row.indexOutputs) == 0 {
			continue
		}
		var outputs map[ObjectAddress]indexRowOutput
		for address, output := range row.indexOutputs {
			old, ok := base.descriptors[address].(*relationDescriptor)
			index, present := next.descriptors[address].(*relationDescriptor)
			if !ok || !present || !output.matches(old) {
				continue
			}
			parent := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			mapped := output
			mapped.index = index
			keysChanged := false
			if output.key.Type() != nil {
				usage, err := indexKeyUsage(parent, index.sqlIndex.keys[0])
				if err != nil {
					return nil, err
				}
				mapped.key, err = reownDomainDatum(output.key, usage.Type, nil, next.scalarImports, next.aclItemImport)
				if err != nil {
					return nil, err
				}
			}
			for key, value := range output.keys {
				usage, err := indexKeyUsage(parent, index.sqlIndex.keys[key])
				if err != nil {
					return nil, err
				}
				rebound, err := reownDomainDatum(value, usage.Type, nil, next.scalarImports, next.aclItemImport)
				if err != nil {
					return nil, err
				}
				if rebound != value {
					if len(mapped.keys) != 0 && &mapped.keys[0] == &output.keys[0] {
						mapped.keys = slices.Clone(output.keys)
					}
					mapped.keys[key] = rebound
					keysChanged = true
				}
			}
			if mapped.index == output.index && mapped.key == output.key && !keysChanged {
				continue
			}
			if outputs == nil {
				outputs = maps.Clone(row.indexOutputs)
			}
			outputs[address] = mapped
		}
		// Retain genuinely stale outputs as stale; never certify/re-execute them.
		if outputs == nil {
			if store.versions[i].indexOutputs != nil {
				continue
			}
			outputs = row.indexOutputs
		}
		if versions == nil {
			versions = slices.Clone(store.versions)
		}
		versions[i].indexOutputs = outputs
	}
	if versions == nil {
		return store, nil
	}
	nextStore := *store
	nextStore.versions = versions
	return &nextStore, nil
}
