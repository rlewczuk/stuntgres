package storage

import (
	"context"
	"math"
	"stuntgres/internal/engine/ids"
)

// HeapTupleFact is a captured physical header with actual transaction provenance.
// A zero Born denotes an uncommitted insertion owned by Inserter; Ender denotes
// a real in-progress UPDATE/DELETE, not a tuple lock. Committed ends use End.
type HeapTupleFact struct {
	Version RowVersion
	Inserter, Ender ids.TxRef
	End ids.CommitSeq
}

// HeapTupleLive implements Dirty (exact) and Vacuum (approximate) admission.
// Vacuum deliberately tests foreign xmin before xmax, unlike the own-xmin
// branch. Both classifiers are independent of the caller's SELECT snapshot.
func HeapTupleLive(f HeapTupleFact, caller ids.TxRef, approximate bool) bool {
	if f.Version.Born == 0 {
		if approximate {
			return f.Inserter == caller && f.Ender == caller && f.Ender != (ids.TxRef{})
		}
		if f.Inserter != caller {
			return true
		}
		return f.Ender != caller || f.Ender == (ids.TxRef{})
	}
	if f.End != 0 {
		return false
	}
	return approximate || f.Ender != caller || f.Ender == (ids.TxRef{})
}

type HeapStatistics struct {
	TableLen, TupleCount, TupleLen, DeadCount, DeadLen, FreeSpace int64
	TuplePercent, DeadPercent, FreePercent, ScannedPercent float64
}

// HeapStatisticsLayout is the shared stateless page model. Every physical item
// consumes a four-byte line pointer plus its eight-aligned allocation. Empty
// storage has no pages; conceptual continuation pages handle large values.
func HeapStatisticsLayout(ctx context.Context, live, dead, liveBytes, deadBytes int64, approximate bool) (HeapStatistics, error) {
	s := HeapStatistics{TupleCount: live, DeadCount: dead, TupleLen: liveBytes, DeadLen: deadBytes}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	count, err := AddSize(live, dead)
	if err != nil { return s, err }
	bytes, err := AddSize(liveBytes, deadBytes)
	if err != nil { return s, err }
	if count > math.MaxInt64/4 {
		_, err = AddSize(math.MaxInt64, 1)
		return s, err
	}
	payload, err := AddSize(bytes, count*4)
	if err != nil { return s, err }
	pages := payload/8168
	if payload%8168 != 0 { pages++ }
	if pages > math.MaxInt64/8192 {
		_, err = AddSize(math.MaxInt64, 1)
		return s, err
	}
	s.TableLen = pages*8192
	s.FreeSpace = pages*8168-payload
	if s.TableLen != 0 {
		percent := func(n int64) float64 {
			v := 100*float64(n)/float64(s.TableLen)
			if !approximate { return math.Round(v*100)/100 }
			return v
		}
		s.TuplePercent = percent(liveBytes)
		s.DeadPercent = percent(deadBytes)
		s.FreePercent = percent(s.FreeSpace)
		s.ScannedPercent = 100
	}
	return s, nil
}

func CaptureHeapStatistics(ctx context.Context, facts []HeapTupleFact, caller ids.TxRef, approximate bool) (HeapStatistics, error) {
	var live, dead, liveBytes, deadBytes int64
	for _, fact := range facts {
		if err := ctx.Err(); err != nil { return HeapStatistics{}, err }
		n, err := RowAllocation(ctx, fact.Version.Values)
		if err != nil { return HeapStatistics{}, err }
		if HeapTupleLive(fact, caller, approximate) {
			live, err = AddSize(live, 1)
			if err == nil { liveBytes, err = AddSize(liveBytes, n) }
		} else {
			dead, err = AddSize(dead, 1)
			if err == nil { deadBytes, err = AddSize(deadBytes, n) }
		}
		if err != nil { return HeapStatistics{}, err }
	}
	return HeapStatisticsLayout(ctx, live, dead, liveBytes, deadBytes, approximate)
}
