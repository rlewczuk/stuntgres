package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func (e *Engine) prepareForeignIndexStorage(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot) (index.RemintedIndexStorage, error) {
	prepared := index.RemintedIndexStorage{Catalog: next, BaseCatalog: e.catalog.State, BaseRow: e.storage.RowFrontier(), BaseVersion: e.storage.VersionFrontier(), Frontier: e.visibility.CommitSeq()}
	var err error
	prepared.Roots, prepared.NextRow, prepared.NextVersion, err = e.prepareRemintedStorage(ctx, next, relations, snapshot, false)
	if err == nil {
		next.SetACLItemImport(nil)
	}
	return prepared, err
}

// prepareRemintedStorage remints the imported storage of next over the
// published engine state. The caller holds Engine.mu.
func (e *Engine) prepareRemintedStorage(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot, preserveProvenance bool) (map[ids.ObjectAddress]*storage.IndexRoot, uint64, uint64, error) {
	base := index.RemintBase{Catalog: e.catalog.State, Relations: e.storage.Relations, Roots: e.storage.IndexRoots, NextRow: e.storage.RowFrontier(), NextVersion: e.storage.VersionFrontier(), Frontier: e.visibility.CommitSeq(), PreserveProvenance: preserveProvenance}
	return index.RemintStorage(ctx, e.catalog.Read(next), base, relations, snapshot)
}
