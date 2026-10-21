package index

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

type RowIdentity struct {
	Row     ids.RowID
	Version ids.VersionID
}

// A validated overlay has a compact physical layout. Baseline installation
// filters its already-produced entries and restores genuine heap positions;
// owner-invisible predecessors have no invented keys.
func (b *AlterIndexBuild) BaselineRoot(ctx context.Context, index *catalog.Relation, baseline *storage.Relation, positions map[RowIdentity]int) (*storage.IndexRoot, error) {
	if b == nil || b.Index == nil || !SameIndexKeySemantics(b.Index, index) || b.Store == nil || baseline == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if b.Store == baseline {
		return b.Root, ctx.Err()
	}
	if b.Root.Hash != nil {
		old := b.Root.Hash
		next := &storage.HashRoot{Buckets: make(map[uint64][]storage.HashEntry), Binding: old.Binding, Storage: baseline.IndexIdentity(), Generation: baseline.Generation, Prefix: len(baseline.Versions), Ends: baseline.Ends}
		for h, bucket := range old.Buckets {
			for _, entry := range bucket {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				position, ok := positions[RowIdentity{Row: entry.Row, Version: entry.Version}]
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
	entries := make([]storage.IndexEntry, 0, len(b.Root.Entries))
	for _, entry := range b.Root.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position, ok := positions[RowIdentity{Row: entry.Row, Version: entry.Version}]
		if !ok {
			continue
		}
		entry.Position = position
		entries = append(entries, entry)
	}
	return NewLogicalIndexRoot(index.SQLIndex, entries), nil
}

func RetainIndexBuild(changes *session.Journal, index *catalog.Relation, baseline *storage.Relation, root *storage.IndexRoot) {
	if JournalIndexesOrZero(changes).UniqueRoots == nil {
		JournalIndexes(changes).UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
	}
	if JournalIndexesOrZero(changes).Candidates == nil {
		JournalIndexes(changes).Candidates = make(map[ids.ObjectAddress]RootCandidate)
	}
	JournalIndexes(changes).UniqueRoots[index.Address()] = root
	JournalIndexes(changes).Candidates[index.Address()] = RootCandidate{Index: index, Root: root, Storage: baseline.IndexIdentity(), Generation: baseline.Generation, Ends: baseline.Ends, Length: len(baseline.Versions)}
}

// Only pending executable index data needs row-coupled outputs. Plain rows keep
// their existing headers/maps and all outputs are frozen before staging.
func AttachIndexBuildOutputs(ctx context.Context, rows []storage.RowVersion, builds map[string]*AlterIndexBuild) ([]storage.RowVersion, bool, error) {
	if len(rows) == 0 {
		return rows, false, nil
	}
	var source *storage.Relation
	for _, build := range builds {
		if len(build.Outputs) != 0 {
			source = build.Store
			break
		}
	}
	if source == nil {
		return rows, false, nil
	}
	positions := make(map[RowIdentity]int, len(source.Versions))
	for i, row := range source.Versions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		positions[RowIdentity{Row: row.Row, Version: row.Version}] = i
	}
	copied := false
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		position, ok := positions[RowIdentity{Row: row.Row, Version: row.Version}]
		if !ok {
			continue
		}
		var outputs map[ids.ObjectAddress]storage.IndexOutput
		for _, build := range builds {
			if len(build.Outputs) == 0 {
				continue
			}
			if build.Store != source || len(build.Outputs) != len(source.Versions) {
				return nil, false, fmt.Errorf("stuntgres: index build output layout changed")
			}
			output := build.Outputs[position]
			if output.Index == nil {
				continue
			}
			if !OutputMatches(output, build.Index) {
				return nil, false, ids.ErrCatalogReplaced
			}
			if outputs == nil {
				outputs = maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
			}
			outputs[build.Index.Address()] = output
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

func BuildPositions(ctx context.Context, store *storage.Relation) (map[RowIdentity]int, error) {
	positions := make(map[RowIdentity]int, len(store.Versions))
	for i, row := range store.Versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		positions[RowIdentity{Row: row.Row, Version: row.Version}] = i
	}
	return positions, nil
}

func BuildVisibleStore(ctx context.Context, store *storage.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) (*storage.Relation, error) {
	if len(writes) == 0 && (deletes == nil || len(deletes.Rows) == 0) {
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
		if !replaced[row.Row] && visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), store.Ends, deletes) {
			live = append(live, row)
		}
	}
	for _, row := range writes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !UniqueRowDeleted(row.Row, deletes) {
			live = append(live, row)
		}
	}
	return &storage.Relation{Versions: live}, nil
}
