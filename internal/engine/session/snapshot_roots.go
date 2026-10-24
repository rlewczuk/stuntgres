package session

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// PrivateStorage is a's storage as journal c sees it over the published
// store: c's replacement if c changed a, else the published relation. A nil
// journal reads the published store. The caller holds Engine.mu.
func PrivateStorage(published *storage.Store, c *Journal, a ids.ObjectAddress) *storage.Relation {
	if c != nil {
		if shape, ok := c.Storage[a]; ok {
			return shape.Replacement
		}
	}
	return published.Relations[a]
}

// CommittedRoots pairs every published storage relation that has a
// published relation descriptor with that descriptor: the immutable roots a
// stable snapshot retains. The caller holds Engine.mu.
func CommittedRoots(state *catalog.State, store *storage.Store) map[ids.ObjectAddress]visibility.Root {
	roots := make(map[ids.ObjectAddress]visibility.Root, len(store.Relations))
	for address, stored := range store.Relations {
		if relation, ok := state.Relation(address); ok {
			roots[address] = visibility.Root{Versions: stored.Versions, Ends: stored.Ends, Storage: stored, Relation: relation}
		}
	}
	return roots
}

// NeedsSnapshotProjection reports whether a stable owner's journal changed
// the column shape of a retained root without a projected snapshot yet. The
// caller holds t.Mu.
func (t *TransactionOwner) NeedsSnapshotProjection(roots map[ids.ObjectAddress]visibility.Root) bool {
	if !t.Isolation.Stable() || t.Catalog == nil {
		return false
	}
	for address, shape := range t.Catalog.Storage {
		if root, exists := roots[address]; exists && !shape.HasSnapshot && !slices.Equal(root.Relation.Attributes, shape.Relation.Attributes) {
			return true
		}
	}
	return false
}
