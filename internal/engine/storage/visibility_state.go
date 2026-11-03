package storage

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/ids"
)

// VisibilityRoot is immutable incarnation-owned VM data. Nil enables rebuilding;
// Invalidated is a persistent explicit empty map, independent of later heap roots.
// Proof is the READ sufficiency horizon, not part of the physical claim basis.
type VisibilityRoot struct {
	Map         VisibilityMap
	Basis       VisibilityBasis
	Proof       VisibilityHorizon
	Invalidated bool
}

var emptyVisibilityRoot = &VisibilityRoot{Invalidated: true}

func EmptyVisibilityRoot() *VisibilityRoot { return emptyVisibilityRoot }

// Persistent comparison keys contain no TxRef/SessionToken owner attachment.
// Every real runtime transaction has a nonzero nonwrapping Diagnostic in its
// tiny opaque Domain/epoch. These keys compare data, never confer authority.
type visibilityTransaction struct {
	Domain     *ids.Domain
	Session    ids.SessionID
	ID         ids.TxID
	Epoch      ids.CatalogEpoch
	Diagnostic uint32
	Present    bool
}

func visibilityTransactionOf(ref ids.TxRef) visibilityTransaction {
	if ref == (ids.TxRef{}) {
		return visibilityTransaction{}
	}
	return visibilityTransaction{ref.Domain(), ref.Session().ID(), ref.ID(), ref.Epoch(), ref.Diagnostic(), true}
}
func (k visibilityTransaction) reusable() bool { return !k.Present || k.Diagnostic != 0 }

type visibilityFact struct {
	Row                                                                           ids.RowID
	Version                                                                       ids.VersionID
	Born, End, ForcedEnd                                                          ids.CommitSeq
	InsertFullXID, DeleteFullXID, RepairStamp                                     uint64
	Inserter, Ender                                                               visibilityTransaction
	Frozen, ImportFrozen, ForcedFrozen, Aborted, LockMaintenance, LinePointerOnly bool
}

func factIdentity(f HeapTupleFact) visibilityFact {
	r := f.Version
	return visibilityFact{r.Row, r.Version, r.Born, f.End, r.ForcedEnd, r.InsertFullXID, r.DeleteFullXID, r.RepairStamp, visibilityTransactionOf(f.Inserter), visibilityTransactionOf(f.Ender), r.Frozen, r.ImportFrozen, r.ForcedFrozen, f.Aborted, f.LockMaintenance, f.LinePointerOnly}
}

// VisibilityBasis captures exact physical roots and payload-free private facts.
// Version identities own immutable payloads; changed shape/store/header roots
// cannot inherit marked byte coordinates from an earlier layout.
type VisibilityBasis struct {
	Rows                []RowVersion
	Ends                *DeletionRoot
	Retained, Discarded *HeapPredecessor
	Headers             *HeapHeaderRoot
	Generation          uint64
	Facts               []visibilityFact
}

func CaptureVisibilityBasis(s *Relation, facts []HeapTupleFact) VisibilityBasis {
	SortVisibilityFacts(facts)
	return PreparedVisibilityFacts(facts).Basis(s)
}

// Basis owns payload-free keys from the prepared canonical vector.
func (facts PreparedVisibilityFacts) Basis(s *Relation) VisibilityBasis {
	b := VisibilityBasis{Rows: s.Versions, Ends: s.Ends, Retained: s.Retained, Discarded: s.Discarded, Headers: s.Headers, Generation: s.Generation, Facts: make([]visibilityFact, len(facts))}
	for i, f := range facts {
		b.Facts[i] = factIdentity(f)
	}
	return b
}
func (b VisibilityBasis) SameRoots(s *Relation) bool {
	return SameVersionRoot(b.Rows, s.Versions) && b.Ends == s.Ends && b.Retained == s.Retained && b.Discarded == s.Discarded && b.Headers == s.Headers && b.Generation == s.Generation
}
func (b VisibilityBasis) Same(s *Relation, facts []HeapTupleFact) bool {
	if !b.SameRoots(s) || len(b.Facts) != len(facts) {
		return false
	}
	for i, f := range facts {
		key := b.Facts[i]
		if !key.Inserter.reusable() || !key.Ender.reusable() || key != factIdentity(f) {
			return false
		}
	}
	return true
}
func SortVisibilityFacts(facts []HeapTupleFact) {
	slices.SortFunc(facts, func(a, b HeapTupleFact) int { return cmp.Compare(a.Version.Version, b.Version.Version) })
}

// SameVisibilityFacts readmits canonical tuple truth. LockMaintenance is a
// separately sampled manager fact; no manager mutex is acquired under the
// owner cohort. Header roots and the full-XID maintenance cutoff fence retirement.
func SameVisibilityFacts(a, b []HeapTupleFact) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// Capture-local readmission still compares exact genuine attachments;
		// invalid diagnostic-zero refs can never borrow a persistent key.
		if a[i].Inserter != b[i].Inserter || a[i].Ender != b[i].Ender {
			return false
		}
		left, right := factIdentity(a[i]), factIdentity(b[i])
		right.LockMaintenance = left.LockMaintenance
		if left != right {
			return false
		}
	}
	return true
}

// SuppressedVisibility preserves the independent current heap-page proof while
// exposing zero VM flags and no mapped extent. It never rebuilds a VM root.
func SuppressedVisibility(heap VisibilityMap) VisibilityMap {
	result := VisibilityMap{Blocks: heap.Blocks, PageRuns: heap.Runs}
	if heap.Blocks != 0 {
		result.Runs = []VisibilityRun{{End: heap.Blocks}}
	}
	return result
}
func (m VisibilityMap) PageVisible(block int64) bool {
	if m.PageRuns == nil {
		return m.Probe(block).AllVisible
	}
	return (VisibilityMap{Runs: m.PageRuns, Blocks: m.Blocks}).Probe(block).AllVisible
}
