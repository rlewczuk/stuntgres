package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
)

// A supplied phase root is a detached candidate fenced against its exact source.
// Catalog publication alone owns installation, revision and root-map authority.
type RootCandidate struct {
	Root       *storage.IndexRoot
	Index      *catalog.Relation
	Storage    *storage.Relation
	Generation uint64
	Ends       *storage.DeletionRoot
	Length     int
}

func (c RootCandidate) Matches(store *storage.Relation) bool {
	return store == c.Storage && store != nil && store.Generation == c.Generation && store.Ends == c.Ends && len(store.Versions) == c.Length
}

func (c RootCandidate) MatchesIndex(index *catalog.Relation, store *storage.Relation) bool {
	return store != nil && c.Index != nil && SameIndexKeySemantics(c.Index, index) && c.Matches(store.IndexIdentity())
}

func SimpleConcurrentIndex(parent *catalog.Relation, definition *catalog.IndexDefinition) (bool, error) {
	for _, key := range definition.Keys {
		_, node, err := CatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		if node != nil {
			return false, nil
		}
	}
	return true, nil
}
