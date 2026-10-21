package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// captureIndexSnapshot captures the indexes of catalog, which e must own.
func (e *Engine) captureIndexSnapshot(ctx context.Context, catalog catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq) (*indexSnapshot, error) {
	return e.captureIndexSnapshotParents(ctx, catalog, relations, roots, frontier, nil, nil)
}

func (e *Engine) captureIndexSnapshotParents(ctx context.Context, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, frontier ids.CommitSeq, parents map[ids.ObjectAddress]*storage.Relation, private *session.Journal) (*indexSnapshot, error) {
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
		if private != nil {
			if root := journalIndexesOrZero(private).uniqueRoots[address]; root != nil {
				return root
			}
		}
		return e.storage.IndexRoots[address]
	}
	snapshot := &indexSnapshot{sources: make(map[ids.ObjectAddress]indexSnapshotSource)}
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
		if relation, ok := descriptor.(*catalog.Relation); ok && partitionNeedsRowHistory(view, relation) {
			store := relations[address]
			if store == nil || store != e.privateStorageLocked(private, address) {
				return nil, ids.ErrCatalogReplaced
			}
			if snapshot.partitionRows == nil {
				snapshot.partitionRows = make(map[ids.ObjectAddress]partitionRowHistory)
			}
			snapshot.partitionRows[address] = partitionRowHistory{relation: relation, store: *store, frontier: frontier}
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		actual, exists := authority.Relation(address)
		actualParent, parentExists := authority.Relation(parent.Address())
		if !exists || !parentExists || !sameIndexKeySemantics(actual, index) || !indexLayoutInputsUnchanged(index, actualParent, parent) {
			return nil, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		store, root := relations[parent.Address()], roots[address]
		if store == nil || root == nil || store != e.privateStorageLocked(private, parent.Address()) || root != actualRoot(address) {
			return nil, ids.ErrCatalogReplaced
		}
		if (root.Hash != nil) != (index.SQLIndex.Method == catalog.HashMethod) {
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
				if len(entry.Keys) != len(index.SQLIndex.Keys) {
					return nil, ids.ErrCatalogReplaced
				}
				if err := check(entry.Row, entry.Version, entry.Position); err != nil {
					return nil, err
				}
			}
		}
		snapshot.sources[address] = indexSnapshotSource{index: index, parent: parent, store: *store, root: root, frontier: frontier}
	}
	return snapshot, nil
}
