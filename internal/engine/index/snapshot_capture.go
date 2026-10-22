package index

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// SnapshotCapture is what CaptureSnapshot reads. The caller fills it under
// Engine.mu after checking the view's owner, epoch and frontier, and holds
// the lock until CaptureSnapshot returns.
type SnapshotCapture struct {
	View      catalog.Read                            // The catalog whose indexes are captured.
	Authority *catalog.State                          // The committed or private catalog they must still match.
	Relations map[ids.ObjectAddress]*storage.Relation // Captured row storage by relation.
	Roots     map[ids.ObjectAddress]*storage.IndexRoot
	Frontier  ids.CommitSeq
	// Parents, when non-nil, selects the indexes of these parents only.
	Parents map[ids.ObjectAddress]*storage.Relation
	// Store and Root return the current (private or committed) store and
	// root the captured ones must be.
	Store func(ids.ObjectAddress) *storage.Relation
	Root  func(ids.ObjectAddress) *storage.IndexRoot
}

// CaptureSnapshot captures the ready, live indexes of c.View with their
// storage and roots, and the row histories partitions need. A captured
// descriptor, store or root that is no longer current is ErrCatalogReplaced.
func CaptureSnapshot(ctx context.Context, c SnapshotCapture) (*Snapshot, error) {
	view, relations, roots, frontier, parents := c.View, c.Relations, c.Roots, c.Frontier, c.Parents
	snapshot := &Snapshot{Sources: make(map[ids.ObjectAddress]SnapshotSource)}
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
		if relation, ok := descriptor.(*catalog.Relation); ok && PartitionNeedsRowHistory(view, relation) {
			store := relations[address]
			if store == nil || store != c.Store(address) {
				return nil, ids.ErrCatalogReplaced
			}
			if snapshot.PartitionRows == nil {
				snapshot.PartitionRows = make(map[ids.ObjectAddress]PartitionRowHistory)
			}
			snapshot.PartitionRows[address] = PartitionRowHistory{Relation: relation, Store: *store, Frontier: frontier}
		}
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !idx.SQLIndex.Ready || !idx.SQLIndex.Live {
			continue
		}
		parent, ok := view.Relation(idx.SQLIndex.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		actual, exists := c.Authority.Relation(address)
		actualParent, parentExists := c.Authority.Relation(parent.Address())
		if !exists || !parentExists || !SameIndexKeySemantics(actual, idx) || !LayoutInputsUnchanged(idx, actualParent, parent) {
			return nil, ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		store, root := relations[parent.Address()], roots[address]
		if store == nil || root == nil || store != c.Store(parent.Address()) || root != c.Root(address) {
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
		snapshot.Sources[address] = SnapshotSource{Index: idx, Parent: parent, Store: *store, Root: root, Frontier: frontier}
	}
	return snapshot, nil
}
