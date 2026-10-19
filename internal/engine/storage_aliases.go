package engine

import "stuntgres/internal/engine/storage"

// Temporary aliases for the declarations that moved to package storage in
// ENGINE-SPLIT step 6b. Step 6c replaces their uses and removes them.

type (
	deletionRoot        = storage.DeletionRoot
	hashIndexBinding    = storage.HashBinding
	hashIndexEntry      = storage.HashEntry
	hashIndexRoot       = storage.HashRoot
	indexRowOutput      = storage.IndexOutput
	logicalIndexEntry   = storage.IndexEntry
	logicalIndexRoot    = storage.IndexRoot
	materializedLineage = storage.MaterializedLineage
	relationStorage     = storage.Relation
	rowEnd              = storage.RowEnd
	rowVersion          = storage.RowVersion
	sequenceGenerator   = storage.SequenceGenerator
	storageStore        = storage.Store
	truncateLineage     = storage.TruncateLineage
)
