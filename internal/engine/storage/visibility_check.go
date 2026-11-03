package storage

import (
	"context"
	"sort"

	"stuntgres/internal/engine/ids"
)

// walkVisibilityLayout is shared by READ classification and consistency checks.
// It visits each genuine physical identity once, including continuation overlap,
// with checked byte coordinates. The preparation boundary owns canonical order.
func walkVisibilityLayout(ctx context.Context, facts PreparedVisibilityFacts, visit func([]HeapTupleFact, int64, int64) error) error {
	var position int64
	for i := 0; i < len(facts); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := i + 1
		for end < len(facts) && facts[end].Version.Version == facts[i].Version.Version {
			end++
		}
		weight := int64(4)
		for _, fact := range facts[i:end] {
			if fact.LinePointerOnly {
				continue
			}
			allocation, err := RowAllocation(ctx, fact.Version.Values)
			if err != nil {
				return err
			}
			weight, err = AddSize(allocation, 4)
			if err != nil {
				return err
			}
			break
		}
		next, err := AddSize(position, weight)
		if err != nil {
			return err
		}
		if err := visit(facts[i:end], position, next); err != nil {
			return err
		}
		position = next
		i = end
	}
	return ctx.Err()
}

// VisibilityCheckHorizon is a conservative running-transaction FULL-XID cutoff.
// Advertised read-only snapshot CommitSeq frontiers are deliberately unrelated.
type VisibilityCheckHorizon struct {
	Oldest  uint64
	Unknown bool
}

func VisibilityContradiction(f HeapTupleFact, horizon VisibilityCheckHorizon, frozen bool, slot HeapSlotState) bool {
	if slot == HeapSlotUnused || slot == HeapSlotRedirect {
		return false
	}
	if slot == HeapSlotDead {
		return true
	}
	r := f.Version
	if frozen {
		return (!r.Frozen && !r.ImportFrozen && !r.ForcedFrozen && uint32(r.InsertFullXID) >= 3) || uint32(r.DeleteFullXID) >= 3 || f.LockMaintenance || f.Ender != (ids.TxRef{})
	}
	end := f.End
	if r.ForcedFrozen {
		end = r.ForcedEnd
	}
	if f.Ender != (ids.TxRef{}) || end != 0 {
		return true
	}
	if !r.ForcedFrozen && (f.Aborted || f.Inserter != (ids.TxRef{})) {
		return true
	}
	if r.Born == 0 && !r.ForcedFrozen {
		return false
	} // Unknown, not a fabricated contradiction.
	return !r.Frozen && !r.ImportFrozen && !r.ForcedFrozen && !horizon.Unknown && r.InsertFullXID != 0 && r.InsertFullXID >= horizon.Oldest
}

func (m VisibilityMap) claimed(first, last int64, frozen bool) bool {
	if first >= m.Blocks {
		return false
	}
	at := sort.Search(len(m.Runs), func(i int) bool { return m.Runs[i].End > first })
	for at < len(m.Runs) && first <= last {
		bits := m.Runs[at].Bits
		if frozen && bits.AllFrozen || !frozen && bits.AllVisible {
			return true
		}
		first = m.Runs[at].End
		at++
	}
	return false
}

// CheckVisibility returns exact VersionIDs, never byte-page/row ordinals. A
// version intersecting several selected pages is emitted once, with no DISTINCT
// cleanup hiding duplicate physical identity. Findings do not change MVCC truth.
func CheckVisibility(ctx context.Context, facts []HeapTupleFact, headers *HeapHeaderRoot, claims VisibilityMap, horizon VisibilityCheckHorizon, frozen bool) ([]ids.VersionID, error) {
	SortVisibilityFacts(facts)
	return PreparedVisibilityFacts(facts).Check(ctx, headers, claims, horizon, frozen)
}

// Check consumes canonical facts without sorting or retaining invocation state.
func (facts PreparedVisibilityFacts) Check(ctx context.Context, headers *HeapHeaderRoot, claims VisibilityMap, horizon VisibilityCheckHorizon, frozen bool) ([]ids.VersionID, error) {
	var findings []ids.VersionID
	err := walkVisibilityLayout(ctx, facts, func(group []HeapTupleFact, start, end int64) error {
		if !claims.claimed(start/8168, (end-1)/8168, frozen) {
			return nil
		}
		for _, fact := range group {
			if VisibilityContradiction(fact, horizon, frozen, headers.Header(fact.Version.Version).Slot) {
				findings = append(findings, fact.Version.Version)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// PreparedVisibilityFacts is one caller-owned canonical vector. Obtain it from
// PrepareVisibilityFacts; consumers may update sampled maintenance, but must not
// append/reorder facts or change VersionIDs before Capture, Check or Basis.
type PreparedVisibilityFacts []HeapTupleFact

// PrepareVisibilityFacts adds only genuine payload-free retained dead slots.
// These consume an existing line pointer, not a fabricated row/XID/payload.
// Generic pgstattuple facts and unused/redirect disposition remain untouched.
func PrepareVisibilityFacts(facts []HeapTupleFact, headers *HeapHeaderRoot) PreparedVisibilityFacts {
	SortVisibilityFacts(facts)
	if headers == nil {
		return PreparedVisibilityFacts(facts)
	}
	payloadCount := len(facts)
	for version, header := range headers.Versions.All() {
		if header.Slot != HeapSlotDead {
			continue
		}
		at := sort.Search(payloadCount, func(i int) bool { return facts[i].Version.Version >= version })
		if at < payloadCount && facts[at].Version.Version == version {
			continue
		}
		facts = append(facts, HeapTupleFact{Version: RowVersion{Version: version}, LinePointerOnly: true})
	}
	if len(facts) != payloadCount {
		SortVisibilityFacts(facts)
	}
	return PreparedVisibilityFacts(facts)
}
