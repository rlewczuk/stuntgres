package dml

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// Slots refer to this command's append-only stage or its captured immutable
// prior-own prefix. Provider hashes only select buckets; matches supplies the
// installed ordering equality, predicate, and expression-key semantics.
type InsertWriteKeyOverlay struct {
	Stage      map[uint64][]int
	Own        map[uint64][]int
	StageCount int
	OwnReady   bool
}

type InsertWriteOverlay struct {
	AffectedRows map[ids.RowID]int
	OwnRowSlots  map[ids.ObjectAddress]map[ids.RowID]int
	Overlays     map[*index.UniqueBoundKey]*InsertWriteKeyOverlay
}
