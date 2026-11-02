package visibility

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// DeleteCandidate is a transaction-private deletion of one row version.
// Base is the predecessor that a private replacement also ends; Ordinal and
// Own carry the write stage's output order and ownership.
type DeleteCandidate struct {
	Ref     ids.ObjectRef
	Storage *storage.Relation
	Row     ids.RowID
	Version ids.VersionID
	Base    ids.VersionID
	Ordinal int
	Own     bool
	// Moved ends the source of a cross-partition UPDATE, not an ordinary DELETE.
	Moved           bool
	EndFullXID      uint64
	RepairStamp     uint64
	BaseRepairStamp uint64
}

// PrivateDeletes owns immutable private deletion facts. Rows is the ordinary
// one-version-per-row fast path; Versions owns supplementary physical siblings.
type PrivateDeletes struct {
	Rows     map[ids.RowID]DeleteCandidate
	Versions map[ids.VersionID]DeleteCandidate
}

// LiveVersion reports whether v is visible at frontier, given committed end
// markers and the private overlay. A private replacement and an older
// retained snapshot may share RowID without
// sharing lineage. End only the deleted version and its actual replacement base.
func LiveVersion(v storage.RowVersion, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *PrivateDeletes) bool {
	return (HeapOperations{}).LiveVersion(v, frontier, ends, own)
}

// LiveVersion applies the ordinary heap's single liveness rule to real versions,
// committed ends and private deletion/replacement overlays.
func (HeapOperations) LiveVersion(v storage.RowVersion, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *PrivateDeletes) bool {
	if own.Ends(v) {
		return false
	}
	if v.ForcedEnd != 0 && v.ForcedEnd <= frontier {
		return false
	}
	if v.ForcedFrozen {
		return true
	}
	if v.Born == 0 {
		return true
	}
	if v.Born > frontier {
		return false
	}
	if end, deleted := ends.Lookup(v.Row, v.Version); deleted && end.End <= frontier {
		return false
	}
	return true
}
