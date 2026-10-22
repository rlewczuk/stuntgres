package storage

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation is one relation's physical storage: one immutable physical head
// per logical row, plus immutable end markers. Published vectors/end roots
// are retained by views; generation guards either.
type Relation struct {
	Versions    []RowVersion
	Ends        *DeletionRoot
	Generation  uint64
	IndexSource *Relation // Origin only on detached index-build captures.
	// Metadata only, accessed under Engine.mu. Rows/end vectors are never
	// changed when this first committed TRUNCATE stamps its predecessor.
	TruncateLineage *TruncateLineage
	// Successful concurrent MV publication stamps both incarnations atomically.
	// It does not share TRUNCATE's current-root/birth-filter semantics.
	MaterializedLineage *MaterializedLineage
	Maintenance         Maintenance
}

// IndexIdentity is the storage an index root was built from: the origin for a
// detached index-build capture, otherwise the storage itself.
func (s *Relation) IndexIdentity() *Relation {
	if s.IndexSource != nil {
		return s.IndexSource
	}
	return s
}

// RowVersion is one physical version of a logical row.
type RowVersion struct {
	Row     ids.RowID
	Version ids.VersionID
	Born    ids.CommitSeq
	// Canonical insertion provenance is distinct from the commit frontier.
	InsertFullXID uint64
	Frozen        bool
	// Detached committed imports have no XID in the target identity universe.
	ImportFrozen bool
	// The last committed key transition survives key restoration and payload
	// replacements. Private successors accumulate transitions until commit.
	KeyChangedAt     ids.CommitSeq
	PendingKeyChange bool
	// Base is the committed predecessor for a private replacement; zero is an insert.
	Base   ids.VersionID
	Values []sqltype.Value
	// Private admission captures index readiness; committed rows clear this pin.
	IndexAdmission *catalog.State
	IndexOutputs   map[ids.ObjectAddress]IndexOutput // Private, immutable produced index data.
}

// RowEnd marks the committed end of one row version. Roots are immutable
// after transfer to a command view or committed storage.
type RowEnd struct {
	Version ids.VersionID
	End     ids.CommitSeq
}

// DeletionRoot holds the committed end markers of a relation, by row.
type DeletionRoot struct{ Rows map[ids.RowID]RowEnd }

// RowByID finds the row with id in rows, trying hint first. A captured
// physical position is only a hint; arbitration is by stable RowID.
func RowByID(rows []RowVersion, id ids.RowID, hint int) (RowVersion, int, bool) {
	if hint >= 0 && hint < len(rows) && rows[hint].Row == id {
		return rows[hint], hint, true
	}
	for i, row := range rows {
		if row.Row == id {
			return row, i, true
		}
	}
	return RowVersion{}, 0, false
}

// SameVersionRoot reports whether a and b are the same version root: equal
// length and, when not empty, the same backing array.
func SameVersionRoot(a, b []RowVersion) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}
