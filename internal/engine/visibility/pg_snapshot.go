package visibility

import (
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CurrentSnapshotLocked captures the full-XID frontier, not writer ordering.
// Caller holds Engine.mu. Completion history wins over stale entry/ticket mirrors.
func (reg *Registry) CurrentSnapshotLocked(self ids.TxRef) (*sqltype.Snapshot, error) {
	completed := reg.latestCompletedXID
	if completed < 2 {
		completed = 2
	}
	xmax, err := NextFullXID(completed)
	if err != nil {
		return nil, err
	}
	xmin := xmax
	if own := reg.Entries[self].FullXID; own != 0 && own < xmin {
		xmin = own
	}
	var members []uint64
	for ref, entry := range reg.Entries {
		id := entry.FullXID
		if ref == self || id == 0 || id >= xmax {
			continue
		}
		record, ok := reg.History[id]
		if !ok || record.State != ids.TxInProgress {
			continue
		}
		members = append(members, id)
		if id < xmin {
			xmin = id
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	return sqltype.NewSnapshot(xmin, xmax, members)
}
