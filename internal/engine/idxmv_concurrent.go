package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
)

// A supplied phase root is a detached candidate fenced against its exact source.
// Catalog publication alone owns installation, revision and root-map authority.
type indexRootCandidate struct {
	root       *storage.IndexRoot
	index      *catalog.Relation
	storage    *storage.Relation
	generation uint64
	ends       *storage.DeletionRoot
	length     int
}

func (c indexRootCandidate) matches(store *storage.Relation) bool {
	return store == c.storage && store != nil && store.Generation == c.generation && store.Ends == c.ends && len(store.Versions) == c.length
}

func (c indexRootCandidate) matchesIndex(index *catalog.Relation, store *storage.Relation) bool {
	return store != nil && c.index != nil && sameIndexKeySemantics(c.index, index) && c.matches(store.IndexIdentity())
}

func simpleConcurrentIndex(parent *catalog.Relation, definition *catalog.IndexDefinition) (bool, error) {
	for _, key := range definition.Keys {
		_, node, err := indexCatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		if node != nil {
			return false, nil
		}
	}
	return true, nil
}
