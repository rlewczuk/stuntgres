package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// captureIndexSnapshot captures the indexes of catalog, which e must own.
func (e *Engine) captureIndexSnapshot(ctx context.Context, catalog catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq) (*index.Snapshot, error) {
	return e.captureIndexSnapshotParents(ctx, catalog, relations, roots, frontier, nil, nil)
}

// captureIndexSnapshotParents checks that view is e's current catalog and
// captures its indexes against the committed or private authority. The
// caller holds Engine.mu.
func (e *Engine) captureIndexSnapshotParents(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq, parents map[ids.ObjectAddress]*storage.Relation, private *session.Journal) (*index.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() == nil || view.Owner() != &e.Catalog.Owner || view.State() == nil || e.Catalog.State.Epoch() != view.Epoch() || frontier != e.Visibility.CommitSeq() {
		return nil, ids.ErrCatalogReplaced
	}
	authority := e.Catalog.State
	if private != nil {
		authority = e.PrivateCatalogLocked(private).State()
	}
	return index.CaptureSnapshot(ctx, index.SnapshotCapture{
		View: view, Authority: authority, Relations: relations, Roots: roots, Frontier: frontier, Parents: parents,
		Store: func(address ids.ObjectAddress) *storage.Relation { return e.PrivateStorageLocked(private, address) },
		Root: func(address ids.ObjectAddress) *storage.IndexRoot {
			return index.EffectiveRoot(e.Storage.IndexRoots, private, address)
		},
	})
}
