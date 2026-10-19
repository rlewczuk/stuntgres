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
}

// PrivateDeletes is a transaction's immutable private deletion overlay for one
// relation, keyed by row.
type PrivateDeletes struct{ Rows map[ids.RowID]DeleteCandidate }

// LiveVersion reports whether v is visible at frontier, given committed end
// markers and the private overlay. A private replacement and an older
// retained snapshot may share RowID without
// sharing lineage. End only the deleted version and its actual replacement base.
func LiveVersion(v storage.RowVersion, frontier ids.CommitSeq, ends *storage.DeletionRoot, own *PrivateDeletes) bool {
	if own != nil {
		if deleted, ok := own.Rows[v.Row]; ok && (v.Version == deleted.Version || deleted.Base != 0 && v.Version == deleted.Base) {
			return false
		}
	}
	if v.Born == 0 {
		return true
	}
	if v.Born > frontier {
		return false
	}
	if ends != nil {
		if end, deleted := ends.Rows[v.Row]; deleted && end.Version == v.Version && end.End <= frontier {
			return false
		}
	}
	return true
}
