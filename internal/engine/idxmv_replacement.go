package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// The final installer consumes completed private data. It owns only the final
// cancellation observation, authority check and indivisible publication.
type remintedIndexStorage struct {
	catalog              *catalog.State
	baseCatalog          *catalog.State
	roots                map[ids.ObjectAddress]*storage.IndexRoot
	nextRow, nextVersion uint64
	baseRow, baseVersion uint64
	frontier             ids.CommitSeq
}
