package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/ids"
)

type indexRowIdentity struct {
	row     ids.RowID
	version ids.VersionID
}

// A validated overlay has a compact physical layout. Baseline installation
// filters its already-produced entries and restores genuine heap positions;
// owner-invisible predecessors have no invented keys.
func (b *alterIndexBuild) baselineRoot(ctx context.Context, index *relationDescriptor, baseline *relationStorage, positions map[indexRowIdentity]int) (*logicalIndexRoot, error) {
	if b == nil || b.index == nil || !sameIndexKeySemantics(b.index, index) || b.store == nil || baseline == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if b.store == baseline {
		return b.root, ctx.Err()
	}
	if b.root.hash != nil {
		old := b.root.hash
		next := &hashIndexRoot{buckets: make(map[uint64][]hashIndexEntry), binding: old.binding, storage: indexStorageIdentity(baseline), generation: baseline.generation, prefix: len(baseline.versions), ends: baseline.ends}
		for h, bucket := range old.buckets {
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := positions[indexRowIdentity{row: entry.row, version: entry.version}]
				if !ok {
					continue
				}
				entry.position = position
				next.buckets[h] = append(next.buckets[h], entry)
			}
		}
		if err := next.captureRowEntries(ctx, index.sqlIndex); err != nil {
			return nil, err
		}
		return &logicalIndexRoot{hash: next}, nil
	}
	entries := make([]logicalIndexEntry, 0, len(b.root.entries))
	for _, entry := range b.root.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position, ok := positions[indexRowIdentity{row: entry.row, version: entry.version}]
		if !ok {
			continue
		}
		entry.position = position
		entries = append(entries, entry)
	}
	return newLogicalIndexRoot(index.sqlIndex, entries), nil
}

func retainIndexBuild(changes *catalogChanges, index *relationDescriptor, baseline *relationStorage, root *logicalIndexRoot) {
	if changes.uniqueRoots == nil {
		changes.uniqueRoots = make(map[ids.ObjectAddress]*logicalIndexRoot)
	}
	if changes.indexCandidates == nil {
		changes.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate)
	}
	changes.uniqueRoots[index.address] = root
	changes.indexCandidates[index.address] = indexRootCandidate{index: index, root: root, storage: indexStorageIdentity(baseline), generation: baseline.generation, ends: baseline.ends, length: len(baseline.versions)}
}

// Only pending executable index data needs row-coupled outputs. Plain rows keep
// their existing headers/maps and all outputs are frozen before staging.
func attachIndexBuildOutputs(ctx context.Context, rows []rowVersion, builds map[string]*alterIndexBuild) ([]rowVersion, bool, error) {
	if len(rows) == 0 {
		return rows, false, nil
	}
	var source *relationStorage
	for _, build := range builds {
		if len(build.outputs) != 0 {
			source = build.store
			break
		}
	}
	if source == nil {
		return rows, false, nil
	}
	positions := make(map[indexRowIdentity]int, len(source.versions))
	for i, row := range source.versions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		positions[indexRowIdentity{row: row.row, version: row.version}] = i
	}
	copied := false
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[indexRowIdentity{row: row.row, version: row.version}]
		if !ok {
			continue
		}
		var outputs map[ids.ObjectAddress]indexRowOutput
		for _, build := range builds {
			if len(build.outputs) == 0 {
				continue
			}
			if build.store != source || len(build.outputs) != len(source.versions) {
				return nil, false, fmt.Errorf("stuntgres: index build output layout changed")
			}
			output := build.outputs[position]
			if output.index == nil {
				continue
			}
			if !output.matches(build.index) {
				return nil, false, ids.ErrCatalogReplaced
			}
			if outputs == nil {
				outputs = maps.Clone(row.indexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]indexRowOutput)
				}
			}
			outputs[build.index.address] = output
		}
		if outputs != nil {
			if !copied {
				rows = slices.Clone(rows)
				copied = true
			}
			rows[i].indexOutputs = outputs
		}
	}
	return rows, copied, nil
}

func indexBuildPositions(ctx context.Context, store *relationStorage) (map[indexRowIdentity]int, error) {
	positions := make(map[indexRowIdentity]int, len(store.versions))
	for i, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		positions[indexRowIdentity{row: row.row, version: row.version}] = i
	}
	return positions, nil
}

func indexBuildVisibleStore(ctx context.Context, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot) (*relationStorage, error) {
	if len(writes) == 0 && (deletes == nil || len(deletes.rows) == 0) {
		return store, nil
	}
	replaced := make(map[ids.RowID]bool, len(writes))
	for _, row := range writes {
		replaced[row.row] = true
	}
	var live []rowVersion
	for _, row := range store.versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !replaced[row.row] && liveVersion(row, ids.CommitSeq(^uint64(0)), store.ends, deletes) {
			live = append(live, row)
		}
	}
	for _, row := range writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !uniqueRowDeleted(row.row, deletes) {
			live = append(live, row)
		}
	}
	return &relationStorage{versions: live}, nil
}
