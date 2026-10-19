package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

type indexRowIdentity struct {
	row     ids.RowID
	version ids.VersionID
}

// A validated overlay has a compact physical layout. Baseline installation
// filters its already-produced entries and restores genuine heap positions;
// owner-invisible predecessors have no invented keys.
func (b *alterIndexBuild) baselineRoot(ctx context.Context, index *catalog.Relation, baseline *storage.Relation, positions map[indexRowIdentity]int) (*storage.IndexRoot, error) {
	if b == nil || b.index == nil || !sameIndexKeySemantics(b.index, index) || b.store == nil || baseline == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if b.store == baseline {
		return b.root, ctx.Err()
	}
	if b.root.Hash != nil {
		old := b.root.Hash
		next := &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: old.Binding, Storage: baseline.IndexIdentity(), Generation: baseline.Generation, Prefix: len(baseline.Versions), Ends: baseline.Ends}
		for h, bucket := range old.Buckets {
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := positions[indexRowIdentity{row: entry.Row, version: entry.Version}]
				if !ok {
					continue
				}
				entry.Position = position
				next.Buckets[h] = append(next.Buckets[h], entry)
			}
		}
		if err := next.CaptureRowEntries(ctx, index.SQLIndex); err != nil {
			return nil, err
		}
		return &storage.IndexRoot{Hash: next}, nil
	}
	entries := make([]storage.IndexEntry, 0, len(b.root.Entries))
	for _, entry := range b.root.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position, ok := positions[indexRowIdentity{row: entry.Row, version: entry.Version}]
		if !ok {
			continue
		}
		entry.Position = position
		entries = append(entries, entry)
	}
	return newLogicalIndexRoot(index.SQLIndex, entries), nil
}

func retainIndexBuild(changes *catalogChanges, index *catalog.Relation, baseline *storage.Relation, root *storage.IndexRoot) {
	if changes.uniqueRoots == nil {
		changes.uniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
	}
	if changes.indexCandidates == nil {
		changes.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate)
	}
	changes.uniqueRoots[index.Address()] = root
	changes.indexCandidates[index.Address()] = indexRootCandidate{index: index, root: root, storage: baseline.IndexIdentity(), generation: baseline.Generation, ends: baseline.Ends, length: len(baseline.Versions)}
}

// Only pending executable index data needs row-coupled outputs. Plain rows keep
// their existing headers/maps and all outputs are frozen before staging.
func attachIndexBuildOutputs(ctx context.Context, rows []storage.RowVersion, builds map[string]*alterIndexBuild) ([]storage.RowVersion, bool, error) {
	if len(rows) == 0 {
		return rows, false, nil
	}
	var source *storage.Relation
	for _, build := range builds {
		if len(build.outputs) != 0 {
			source = build.store
			break
		}
	}
	if source == nil {
		return rows, false, nil
	}
	positions := make(map[indexRowIdentity]int, len(source.Versions))
	for i, row := range source.Versions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		positions[indexRowIdentity{row: row.Row, version: row.Version}] = i
	}
	copied := false
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[indexRowIdentity{row: row.Row, version: row.Version}]
		if !ok {
			continue
		}
		var outputs map[ids.ObjectAddress]storage.IndexOutput
		for _, build := range builds {
			if len(build.outputs) == 0 {
				continue
			}
			if build.store != source || len(build.outputs) != len(source.Versions) {
				return nil, false, fmt.Errorf("stuntgres: index build output layout changed")
			}
			output := build.outputs[position]
			if output.Index == nil {
				continue
			}
			if !indexOutputMatches(output, build.index) {
				return nil, false, ids.ErrCatalogReplaced
			}
			if outputs == nil {
				outputs = maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
			}
			outputs[build.index.Address()] = output
		}
		if outputs != nil {
			if !copied {
				rows = slices.Clone(rows)
				copied = true
			}
			rows[i].IndexOutputs = outputs
		}
	}
	return rows, copied, nil
}

func indexBuildPositions(ctx context.Context, store *storage.Relation) (map[indexRowIdentity]int, error) {
	positions := make(map[indexRowIdentity]int, len(store.Versions))
	for i, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		positions[indexRowIdentity{row: row.Row, version: row.Version}] = i
	}
	return positions, nil
}

func indexBuildVisibleStore(ctx context.Context, store *storage.Relation, writes []storage.RowVersion, deletes *privateDeletionRoot) (*storage.Relation, error) {
	if len(writes) == 0 && (deletes == nil || len(deletes.rows) == 0) {
		return store, nil
	}
	replaced := make(map[ids.RowID]bool, len(writes))
	for _, row := range writes {
		replaced[row.Row] = true
	}
	var live []storage.RowVersion
	for _, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !replaced[row.Row] && liveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, deletes) {
			live = append(live, row)
		}
	}
	for _, row := range writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !uniqueRowDeleted(row.Row, deletes) {
			live = append(live, row)
		}
	}
	return &storage.Relation{Versions: live}, nil
}
