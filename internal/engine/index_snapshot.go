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

func (e *Engine) captureIndexSnapshotParents(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq, parents map[ids.ObjectAddress]*storage.Relation, private *session.Journal) (*index.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if view.Owner() == nil || view.Owner() != e.catalog.owner() || view.State() == nil || e.catalog.state.Epoch() != view.Epoch() || frontier != e.visibility.CommitSeq() {
		return nil, ids.ErrCatalogReplaced
	}
	authority := e.catalog.state
	if private != nil {
		authority = e.privateCatalogLocked(private).State()
	}
	actualRoot := func(address ids.ObjectAddress) *storage.IndexRoot {
		return index.EffectiveRoot(e.storage.IndexRoots, private, address)
	}
	snapshot := &index.Snapshot{Sources: make(map[ids.ObjectAddress]index.SnapshotSource)}
	for address, descriptor := range view.Descriptors() {
		if parents != nil {
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.SQLIndex == nil || parents[index.SQLIndex.Parent] == nil {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && index.PartitionNeedsRowHistory(view, relation) {
			store := relations[address]
			if store == nil || store != e.privateStorageLocked(private, address) {
				return nil, ids.ErrCatalogReplaced
			}
			if snapshot.PartitionRows == nil {
				snapshot.PartitionRows = make(map[ids.ObjectAddress]index.PartitionRowHistory)
			}
			snapshot.PartitionRows[address] = index.PartitionRowHistory{Relation: relation, Store: *store, Frontier: frontier}
		}
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !idx.SQLIndex.Ready || !idx.SQLIndex.Live {
			continue
		}
		parent, ok := view.Relation(idx.SQLIndex.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		actual, exists := authority.Relation(address)
		actualParent, parentExists := authority.Relation(parent.Address())
		if !exists || !parentExists || !index.SameIndexKeySemantics(actual, idx) || !index.LayoutInputsUnchanged(idx, actualParent, parent) {
			return nil, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		store, root := relations[parent.Address()], roots[address]
		if store == nil || root == nil || store != e.privateStorageLocked(private, parent.Address()) || root != actualRoot(address) {
			return nil, ids.ErrCatalogReplaced
		}
		if (root.Hash != nil) != (idx.SQLIndex.Method == catalog.HashMethod) {
			return nil, ids.ErrCatalogReplaced
		}
		if root.Hash != nil && (root.Hash.Storage != store.IndexIdentity() || root.Hash.Generation != store.Generation || root.Hash.Ends != store.Ends || root.Hash.Prefix != len(store.Versions)) {
			return nil, ids.ErrCatalogReplaced
		}
		check := func(row ids.RowID, version ids.VersionID, position int) error {
			if position < 0 || position >= len(store.Versions) {
				return ids.ErrCatalogReplaced
			}
			actual := store.Versions[position]
			if actual.Row != row || actual.Version != version {
				return ids.ErrCatalogReplaced
			}
			return nil
		}
		if root.Hash != nil {
			for _, bucket := range root.Hash.Buckets {
				for _, entry := range bucket {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if err := check(entry.Row, entry.Version, entry.Position); err != nil {
						return nil, err
					}
				}
			}
		} else {
			for _, entry := range root.Entries {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if len(entry.Keys) != len(idx.SQLIndex.Keys) {
					return nil, ids.ErrCatalogReplaced
				}
				if err := check(entry.Row, entry.Version, entry.Position); err != nil {
					return nil, err
				}
			}
		}
		snapshot.Sources[address] = index.SnapshotSource{Index: idx, Parent: parent, Store: *store, Root: root, Frontier: frontier}
	}
	return snapshot, nil
}
