package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Captured with the existing replacement source mutex/frontier. Source versions
// and datum payloads remain immutable; candidate copyRows owns separate vectors.
type partitionRowHistory struct {
	relation *catalog.Relation
	store    storage.Relation
	frontier ids.CommitSeq
}

func partitionNeedsRowHistory(catalog catalog.Read, d *catalog.Relation) bool {
	if d.Membership == nil || d.Partition != nil {
		return false
	}
	for _, check := range d.Constraints.Check {
		if check.NotValid {
			return partitionHashReplayTarget(catalog, d) != d
		}
	}
	return false
}

func (h partitionRowHistory) nextRow(ctx context.Context, position *int, candidate storage.RowVersion) ([]sqltype.Value, error) {
	for *position < len(h.store.Versions) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		original := h.store.Versions[*position]
		*position = *position + 1
		if !visibility.LiveVersion(original, h.frontier, h.store.Ends, nil) {
			continue
		}
		if original.Row != candidate.Row || original.Version != candidate.Version {
			return nil, ids.ErrCatalogReplaced
		}
		return original.Values, nil
	}
	return nil, ids.ErrCatalogReplaced
}
