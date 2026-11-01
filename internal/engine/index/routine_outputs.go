package index

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Only pending writes already participating in this publication are visited.
// Replacement with no row publication visits no rows. A physical rebuild owns
// fresh evaluation and cannot present transported old output as fresh truth.
func preserveRoutineOutputs(publications []RowPublication, candidates map[ids.ObjectAddress]RootCandidate, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation) ([]RowPublication, error) {
	if len(publications) == 0 || len(candidates) == 0 {
		return publications, nil
	}
	needed := false
	for _, candidate := range candidates {
		if candidate.routineSource != nil {
			needed = true
			break
		}
	}
	if !needed {
		return publications, nil
	}
	var result []RowPublication
	for i, publication := range publications {
		var writes []storage.RowVersion
		for j, row := range publication.Writes {
			var outputs map[ids.ObjectAddress]storage.IndexOutput
			for address, output := range row.IndexOutputs {
				candidate := candidates[address]
				receipt := candidate.routineSource
				if receipt == nil {
					continue
				}
				plain := candidate
				plain.routineSource = nil
				if plain != receipt.candidate || candidate.Index == nil || candidate.Index.SQLIndex == nil {
					return nil, ids.ErrCatalogReplaced
				}
				if rebuildParents[publication.Address] || rebuildIndexes[address] != nil {
					continue
				}
				if candidate.Index.SQLIndex.Parent != publication.Address {
					return nil, ids.ErrCatalogReplaced
				}
				if OutputMatches(output, candidate.Index) {
					continue
				}
				authenticated := false
				for source := receipt; source != nil; source = source.previous {
					if SameIndexKeySemantics(output.Index, source.source) {
						authenticated = true
						break
					}
				}
				if !authenticated {
					return nil, ids.ErrCatalogReplaced
				}
				// Member (including false), keys, row/version and producer backing remain
				// unchanged. Only the proven index recipe identity is reminted.
				output.Index = candidate.Index
				if outputs == nil {
					outputs = maps.Clone(row.IndexOutputs)
				}
				outputs[address] = output
			}
			if outputs != nil {
				if writes == nil {
					writes = slices.Clone(publication.Writes)
				}
				row.IndexOutputs = outputs
				writes[j] = row
			}
		}
		if writes != nil {
			if result == nil {
				result = slices.Clone(publications)
			}
			publication.Writes = writes
			result[i] = publication
		}
	}
	if result != nil {
		return result, nil
	}
	return publications, nil
}
