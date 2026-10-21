package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// Slots refer to this command's append-only stage or its captured immutable
// prior-own prefix. Provider hashes only select buckets; matches supplies the
// installed ordering equality, predicate, and expression-key semantics.
type insertWriteKeyOverlay struct {
	stage      map[uint64][]int
	own        map[uint64][]int
	stageCount int
	ownReady   bool
}

type insertWriteOverlay struct {
	affectedRows map[ids.RowID]int
	ownRowSlots  map[ids.ObjectAddress]map[ids.RowID]int
	overlays     map[*index.UniqueBoundKey]*insertWriteKeyOverlay
}
