package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// The final installer consumes completed private data. It owns only the final
// cancellation observation, authority check and indivisible publication.
type RemintedIndexStorage struct {
	Catalog              *catalog.State
	BaseCatalog          *catalog.State
	Roots                map[ids.ObjectAddress]*storage.IndexRoot
	NextRow, NextVersion uint64
	BaseRow, BaseVersion uint64
	Frontier             ids.CommitSeq
}
