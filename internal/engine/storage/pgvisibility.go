package storage

import (
	"context"
	"math"
	"sort"

	"stuntgres/internal/engine/ids"
)

// VisibilityHorizon is detached CommitSeq protection, never an Xmin/XID.
// Protected distinguishes a genuine frontier zero from no advertised snapshot.
type VisibilityHorizon struct {
	Frontier           ids.CommitSeq
	Protected, Unknown bool
}
type VisibilityBits struct{ AllVisible, AllFrozen bool }
type VisibilityRun struct {
	End  int64 // Exclusive heap block; adjacent equal classifications coalesce.
	Bits VisibilityBits
}

// VisibilityMap owns compressed immutable block results, not captured heap roots.
// Both scalar probes and the SRF/summary use precisely this classification.
type VisibilityMap struct {
	Runs                                        []VisibilityRun
	Blocks, MappedBlocks, AllVisible, AllFrozen int64
	// PageRuns is an independent current heap PD proof; nil uses Runs.
	// Persistent roots never borrow it from another capture.
	PageRuns []VisibilityRun
}

func (m VisibilityMap) Probe(block int64) VisibilityBits {
	if block < 0 || block >= m.Blocks {
		return VisibilityBits{}
	}
	i := sort.Search(len(m.Runs), func(i int) bool { return m.Runs[i].End > block })
	return m.Runs[i].Bits
}
func (m *VisibilityMap) append(end int64, bits VisibilityBits) {
	count := end - m.Blocks
	if bits.AllVisible {
		m.AllVisible += count
	}
	if bits.AllFrozen {
		m.AllFrozen += count
	}
	if len(m.Runs) != 0 && m.Runs[len(m.Runs)-1].Bits == bits {
		m.Runs[len(m.Runs)-1].End = end
	} else {
		m.Runs = append(m.Runs, VisibilityRun{End: end, Bits: bits})
	}
	m.Blocks = end
}

// VisibilityVMExtent models a VM page's 8168 payload bytes, four heap blocks
// per byte. The caller supplies the highest block reached by a genuine frozen
// physical header, regardless of the current universal-visibility proof.
func VisibilityVMExtent(blocks int64) (int64, error) {
	if blocks < 0 {
		_, err := AddSize(blocks, 0)
		return 0, err
	}
	pages := blocks / 32672
	if blocks%32672 != 0 {
		pages++
	}
	if pages > math.MaxInt64/32672 {
		_, err := AddSize(math.MaxInt64, 1)
		return 0, err
	}
	return pages * 32672, nil
}
func VisibilityTupleBits(f HeapTupleFact, horizon VisibilityHorizon, unknownLocks bool) VisibilityBits {
	if f.LinePointerOnly {
		return VisibilityBits{}
	}
	row := f.Version
	visible := row.Born != 0 && f.Inserter == (ids.TxRef{}) && f.Ender == (ids.TxRef{}) && f.End == 0 && (row.Frozen || row.ImportFrozen) && !horizon.Unknown && (!horizon.Protected || row.Born <= horizon.Frontier)
	return VisibilityBits{AllVisible: visible, AllFrozen: visible && !unknownLocks && !f.LockMaintenance}
}

// CaptureVisibility consumes the caller-owned detached fact vector, sorting it
// by genuine VersionID without copying values. Duplicate identities consume one
// allocation and combine conservatively; distinct physical predecessors remain.
func CaptureVisibility(ctx context.Context, facts []HeapTupleFact, horizon VisibilityHorizon, unknownLocks bool) (VisibilityMap, error) {
	SortVisibilityFacts(facts)
	return PreparedVisibilityFacts(facts).Capture(ctx, horizon, unknownLocks)
}

// Capture classifies a prepared owned vector without repeating canonicalization.
func (facts PreparedVisibilityFacts) Capture(ctx context.Context, horizon VisibilityHorizon, unknownLocks bool) (VisibilityMap, error) {
	var result VisibilityMap
	var position, frozenBlocks int64
	current := VisibilityBits{AllVisible: true, AllFrozen: true}
	err := walkVisibilityLayout(ctx, facts, func(group []HeapTupleFact, start, end int64) error {
		bits := VisibilityBits{AllVisible: true, AllFrozen: true}
		frozen := false
		for _, fact := range group {
			proof := VisibilityTupleBits(fact, horizon, unknownLocks)
			bits.AllVisible = bits.AllVisible && proof.AllVisible
			bits.AllFrozen = bits.AllFrozen && proof.AllFrozen
			frozen = frozen || fact.Version.Frozen || fact.Version.ImportFrozen
		}
		if frozen {
			frozenBlocks = end / 8168
			if end%8168 != 0 {
				frozenBlocks++
			}
		}
		remaining := end - start
		take := min(8168-position%8168, remaining)
		current.AllVisible = current.AllVisible && bits.AllVisible
		current.AllFrozen = current.AllFrozen && bits.AllFrozen
		position += take
		remaining -= take
		if position%8168 == 0 {
			result.append(position/8168, current)
			current = VisibilityBits{AllVisible: true, AllFrozen: true}
		}
		if remaining >= 8168 {
			whole := remaining / 8168
			position += whole * 8168
			remaining %= 8168
			result.append(position/8168, bits)
		}
		if remaining != 0 {
			current = bits
			position += remaining
		}
		return nil
	})
	if err != nil {
		return VisibilityMap{}, err
	}
	if position%8168 != 0 {
		result.append(position/8168+1, current)
	}
	result.MappedBlocks, err = VisibilityVMExtent(frozenBlocks)
	if err != nil {
		return VisibilityMap{}, err
	}
	return result, nil
}

// CatalogVisibility uses the actual producer's accumulated RowAllocation+4
// bytes. Unknown row provenance cannot establish positive flags or a VM page.
func CatalogVisibility(bytes int64) (VisibilityMap, error) {
	if _, err := AddSize(bytes, 0); err != nil {
		return VisibilityMap{}, err
	}
	blocks := bytes / 8168
	if bytes%8168 != 0 {
		blocks++
	}
	var result VisibilityMap
	if blocks != 0 {
		result.append(blocks, VisibilityBits{})
	}
	return result, nil
}
