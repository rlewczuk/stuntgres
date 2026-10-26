package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// A compact private build's produced outputs are mapped to baseline slots before
// summarizing. No SQL recipe is replayed and no source range ordinal is reused.
func (b *AlterIndexBuild) brinBaselineRoot(evaluation *exec.Context, index *catalog.Relation, baseline *storage.Relation, positions map[RowIdentity]int) (*storage.IndexRoot, error) {
	width, _ := BrinOptions(index)
	root := &storage.BrinRoot{EffectivePagesPerRange: width, Bindings: b.Root.Brin.Bindings, Storage: baseline.IndexIdentity(), Generation: baseline.Generation, Prefix: len(baseline.Versions), Ends: baseline.Ends, Ranges: make([]storage.BrinRange, (len(baseline.Versions)+width-1)/width)}
	for i := range root.Ranges {
		root.Ranges[i] = storage.BrinRange{Summarized: true, Keys: make([]storage.BrinSummary, len(root.Bindings))}
	}
	for i, row := range b.Store.Versions {
		if err := evaluation.Context.Err(); err != nil {
			return nil, err
		}
		if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), b.Store.Ends, nil) {
			continue
		}
		slot, ok := positions[RowIdentity{Row: row.Row, Version: row.Version}]
		if !ok {
			continue
		}
		var output storage.IndexOutput
		if len(b.Outputs) != 0 {
			output = b.Outputs[i]
			if !output.Member {
				continue
			}
		}
		for key, binding := range root.Bindings {
			value := output.Key
			if len(b.Outputs) != 0 {
				value = output.Keys[key]
			} else {
				if binding.Slot < 0 {
					return nil, ids.ErrCatalogReplaced
				}
				value = row.Values[binding.Slot]
			}
			if err := extendBrinSummary(evaluation, &root.Ranges[slot/width].Keys[key], binding, value); err != nil {
				return nil, err
			}
		}
	}
	return &storage.IndexRoot{Brin: root}, evaluation.Context.Err()
}
