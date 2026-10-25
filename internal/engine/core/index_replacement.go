package core

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func (e *Engine) prepareForeignIndexStorage(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot) (index.RemintedIndexStorage, error) {
	prepared := index.RemintedIndexStorage{Catalog: next, BaseCatalog: e.Catalog.State, BaseRow: e.Storage.RowFrontier(), BaseVersion: e.Storage.VersionFrontier(), Frontier: e.Visibility.CommitSeq()}
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
	base := index.RemintBase{Catalog: e.Catalog.State, Relations: e.Storage.Relations, Roots: e.Storage.IndexRoots, NextRow: e.Storage.RowFrontier(), NextVersion: e.Storage.VersionFrontier(), Frontier: e.Visibility.CommitSeq(), PreserveProvenance: preserveProvenance}
	return index.RemintStorage(ctx, e.Catalog.Read(next), base, relations, snapshot)
}
