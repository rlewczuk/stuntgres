package dml

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
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

type writeVersionIdentity struct {
	Version     ids.VersionID
	RepairStamp uint64
}

func writeIdentity(row storage.RowVersion) writeVersionIdentity {
	return writeVersionIdentity{row.Version, row.RepairStamp}
}

type InsertWriteOverlay struct {
	AffectedVersions map[writeVersionIdentity]int
	OwnVersionSlots  map[ids.ObjectAddress]map[writeVersionIdentity]int
	Overlays         map[*index.UniqueBoundKey]*InsertWriteKeyOverlay
}

func (o *InsertWriteOverlay) AffectedSlot(row storage.RowVersion) (int, bool) {
	slot, ok := o.AffectedVersions[writeIdentity(row)]
	return slot, ok
}
