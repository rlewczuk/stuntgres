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
	Versions []RowVersion
	Ends     *DeletionRoot
	Retained *HeapPredecessor
	// Discarded owns genuine aborted physical versions, never visible without
	// an exact-version repair. Headers is replaced, never mutated in place.
	Discarded   *HeapPredecessor
	Headers     *HeapHeaderRoot
	Generation  uint64
	IndexSource *Relation // Origin only on detached index-build captures.
	// Metadata only, accessed under Engine.mu. Rows/end vectors are never
	// changed when this first committed TRUNCATE stamps its predecessor.
	TruncateLineage *TruncateLineage
	// Successful concurrent MV publication stamps both incarnations atomically.
	// It does not share TRUNCATE's current-root/birth-filter semantics.
	MaterializedLineage *MaterializedLineage
	// Read-routing provenance, immutable once published. Preserving FULL/MV
	// replacements carry the family; genuine TRUNCATE advances only the new
	// store's boundary to its actual commit frontier. Destructive replacements
	// and imports start a new family. Neither field retains predecessor memory.
	RetainedOrigin   ids.OID
	TruncateBoundary ids.CommitSeq
	Maintenance      Maintenance
	// Visibility is immutable modeled VM authority for this incarnation.
	// Physical truncation publishes outside ordinary transaction undo.
	Visibility *VisibilityRoot
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
	DeleteFullXID uint64
	// HeapKilled projects irreversible exact slot death from captured Headers.
	// It is not an independent visibility or transaction undo authority.
	HeapKilled bool
	// ForcedFrozen bypasses insertion/end visibility for this exact physical
	// version. Ordinary maintenance Frozen does not bypass a committed end.
	ForcedFrozen bool
	RepairStamp  uint64
	ForcedEnd    ids.CommitSeq
	Frozen       bool
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
	// PrivateHistory is an immutable chain of actual superseded versions.
	// It travels with Writes through statement, savepoint and prepared ownership.
	// Published heads clear it; Relation.Retained owns committed predecessors.
	PrivateHistory *HeapPredecessor
}

// RowEnd marks the committed end of one row version. Roots are immutable
// after transfer to a command view or committed storage.
type RowEnd struct {
	Row        ids.RowID
	Version    ids.VersionID
	End        ids.CommitSeq
	EndFullXID uint64
	Moved      bool // Cross-partition UPDATE; waiters must not treat it as DELETE.
}

// DeletionRoot owns committed ends. Rows is the single-version-per-row fast
// path; Versions owns only supplementary same-RowID physical-version ends.
// Published roots are immutable; use the exact-version methods for all access.
type DeletionRoot struct {
	Rows     map[ids.RowID]RowEnd
	Versions map[ids.VersionID]RowEnd
}

// HeapPredecessor owns an immutable version header and genuine end facts.
// Values remain shared immutable payloads. End zero belongs to the private
// writer carrying this root; publication stamps detached nodes before transfer.
type HeapPredecessor struct {
	Version    RowVersion
	End        ids.CommitSeq
	EndFullXID uint64
	Next       *HeapPredecessor
}

func RetainHeapPredecessor(row RowVersion) *HeapPredecessor {
	next := row.PrivateHistory
	row.PrivateHistory = nil
	return &HeapPredecessor{Version: row, Next: next}
}

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

// MapHeapPredecessors preserves the identical root on a no-op and shares the
// unchanged suffix on a type/layout transport. The mapper runs once per actual
// header; neither published nodes nor their payloads are mutated.
func MapHeapPredecessors(root *HeapPredecessor, mapper func(RowVersion) (RowVersion, bool, error)) (*HeapPredecessor, error) {
	var updates map[*HeapPredecessor]RowVersion
	var last *HeapPredecessor
	for prior := root; prior != nil; prior = prior.Next {
		row, changed, err := mapper(prior.Version)
		if err != nil {
			return nil, err
		}
		if changed {
			if updates == nil {
				updates = make(map[*HeapPredecessor]RowVersion)
			}
			updates[prior], last = row, prior
		}
	}
	if last == nil {
		return root, nil
	}
	var next *HeapPredecessor
	tail := &next
	for prior := root; ; prior = prior.Next {
		copy := *prior
		if row, ok := updates[prior]; ok {
			copy.Version = row
		}
		*tail = &copy
		if prior == last {
			break
		}
		tail = &copy.Next
	}
	return next, nil
}
