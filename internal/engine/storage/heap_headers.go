package storage

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/util"
)

// LogicalTID is the single bijection used by system attributes and tuple-lock
// inspection. Allocated version identities are never synthesized from a row ID.
func LogicalTID(version ids.VersionID) (uint32, uint16, error) {
	if version == 0 || uint64(version) > uint64(math.MaxUint32)*65535 {
		return 0, 0, fmt.Errorf("stuntgres: logical row version exceeds tid range")
	}
	n := uint64(version) - 1
	return uint32(n / 65535), uint16(n%65535 + 1), nil
}

func VersionFromTID(block uint32, offset uint16) (ids.VersionID, bool) {
	if block == math.MaxUint32 || offset == 0 {
		return 0, false
	}
	return ids.VersionID(uint64(block)*65535 + uint64(offset)), true
}

// HeapXmin exposes raw xmin, not the frozen visibility interpretation.
// Ordinary VACUUM preserves the inserting XID; surgery and detached imports
// explicitly represent a raw FrozenTransactionId instead.
func (v RowVersion) HeapXmin() uint32 {
	if v.ForcedFrozen || v.ImportFrozen {
		return 2
	}
	return uint32(v.InsertFullXID)
}
func (v RowVersion) HeapXmax() uint32 {
	return uint32(v.DeleteFullXID)
}

// BaseHeaderStamp is the actual committed-base admission captured by a private
// successor chain. A newer repair of that base cannot be undone by this writer.
func (v RowVersion) BaseHeaderStamp() uint64 {
	for prior := v.PrivateHistory; prior != nil; prior = prior.Next {
		if prior.Version.Version == v.Base {
			return prior.Version.RepairStamp
		}
	}
	return 0
}

type HeapSlotState uint8

const (
	HeapSlotNormal HeapSlotState = iota
	HeapSlotRedirect
	HeapSlotDead
	HeapSlotUnused
)

// HeapHeader is physical-version metadata, not a logical-row visibility flag.
// RepairStamp distinguishes a preexisting writer from DML admitted after repair.
type HeapHeader struct {
	Xmax             uint64
	Frozen, Repaired bool
	RepairStamp      uint64
	// The zero pair uses ordinary RowID arbitration. Repaired descendants
	// retain their exact repair anchor without growing every RowVersion.
	TupleLockVersion ids.VersionID
	TupleLockStamp   uint64
	End              ids.CommitSeq
	Slot             HeapSlotState
	Redirect         ids.VersionID
	Successor        ids.VersionID // Zero is the slot's self-link, including after repair.
}

// HeapHeaderRoot is immutable after publication. Additional retains only real
// repaired versions no longer guaranteed to be in the ordinary head vector.
// Tuple payload backing remains shared with its canonical heap version.
// Versions shares unchanged trie nodes across publications; each exact header
// update copies only its search path, including ordinary per-row UPDATE staging.
type HeapHeaderRoot struct {
	Versions   util.PersistentMap[ids.VersionID, HeapHeader]
	Additional []RowVersion
	Stamp      uint64
}

func (h *HeapHeaderRoot) Apply(v RowVersion) RowVersion {
	if h != nil {
		if header, ok := h.Versions.Get(v.Version); ok {
			v.DeleteFullXID = header.Xmax
			v.Frozen = v.Frozen || header.Frozen
			v.ForcedFrozen = header.Repaired
			v.RepairStamp = header.RepairStamp
			v.ForcedEnd = header.End
			v.HeapKilled = header.Slot != HeapSlotNormal
		}
	}
	return v
}

func (h *HeapHeaderRoot) Header(version ids.VersionID) HeapHeader {
	if h == nil {
		return HeapHeader{}
	}
	header, _ := h.Versions.Get(version)
	return header
}

// KillHeapHeaders publishes exact slot death without touching payloads, repair
// identities, other header fields or indexes. Repeated/empty selections share
// the original root; callers fence the relation generation before publication.
func KillHeapHeaders(previous *HeapHeaderRoot, rows []RowVersion) *HeapHeaderRoot {
	var next *HeapHeaderRoot
	for _, row := range rows {
		current := previous
		if next != nil {
			current = next
		}
		header := HeapHeader{Xmax: row.DeleteFullXID, Frozen: row.Frozen, Repaired: row.ForcedFrozen, RepairStamp: row.RepairStamp, End: row.ForcedEnd}
		if current != nil {
			if existing, ok := current.Versions.Get(row.Version); ok {
				header = existing
			}
		}
		if header.Slot != HeapSlotNormal {
			continue
		}
		if next == nil {
			next = &HeapHeaderRoot{}
			if previous != nil {
				*next = *previous
			}
		}
		header.Slot = HeapSlotDead
		next.Versions = next.Versions.Set(row.Version, header)
	}
	if next == nil {
		return previous
	}
	return next
}

// RepairHeapHeaders prepares one immutable publication for a completed block.
// Caller has validated each supplied normal slot against actual heap/owner roots.
func RepairHeapHeaders(previous *HeapHeaderRoot, rows []RowVersion) (*HeapHeaderRoot, error) {
	first := 0
	for first < len(rows) && previous.Header(rows[first].Version).Slot != HeapSlotNormal {
		first++
	}
	if first == len(rows) {
		return previous, nil
	}
	rows = rows[first:]
	if previous != nil && previous.Stamp == math.MaxUint64 {
		return nil, fmt.Errorf("stuntgres: heap repair identity exhausted")
	}
	next := &HeapHeaderRoot{Stamp: 1}
	if previous != nil {
		next.Versions = previous.Versions
		next.Stamp = previous.Stamp + 1
		next.Additional = append([]RowVersion(nil), previous.Additional...)
	}
	known := make(map[ids.VersionID]bool, len(next.Additional)+len(rows))
	for _, row := range next.Additional {
		known[row.Version] = true
	}
	for _, row := range rows {
		if previous.Header(row.Version).Slot != HeapSlotNormal {
			continue
		}
		next.Versions = next.Versions.Set(row.Version, HeapHeader{Frozen: true, Repaired: true, RepairStamp: next.Stamp, TupleLockVersion: row.Version, TupleLockStamp: next.Stamp})
		if !known[row.Version] {
			row.PrivateHistory = nil
			row.IndexAdmission, row.IndexOutputs = nil, nil
			next.Additional = append(next.Additional, row)
			known[row.Version] = true
		}
	}
	return next, nil
}

// EndHeapHeaders records real physical xmax without mutating captured roots.
// Writers admitted before a repair cannot restore the visibility bits it cleared.
func EndHeapHeaders(previous *HeapHeaderRoot, rows []RowVersion, xid uint64) *HeapHeaderRoot {
	return endHeapHeaders(previous, rows, xid, 0, false)
}

// endHeapHeaders installs raw xmax and optional commit ends in the same immutable
// publication, rather than copying each changed trie path twice at commit.
func endHeapHeaders(previous *HeapHeaderRoot, rows []RowVersion, xid uint64, end ids.CommitSeq, committed bool) *HeapHeaderRoot {
	if len(rows) == 0 {
		return previous
	}
	var next *HeapHeaderRoot
	for _, row := range rows {
		header := previous.Header(row.Version)
		if row.RepairStamp != header.RepairStamp {
			continue
		}
		if next == nil {
			next = &HeapHeaderRoot{}
			if previous != nil {
				next.Versions = previous.Versions
				next.Additional, next.Stamp = previous.Additional, previous.Stamp
			}
		}
		header.Xmax = xid
		if committed {
			header.End = end
		}
		next.Versions = next.Versions.Set(row.Version, header)
	}
	if next == nil {
		return previous
	}
	return next
}

// CommitHeapEnds only finalizes DML admitted against the current repair stamp.
// A peer's pre-repair commit cannot reintroduce its cleared deletion header.
func CommitHeapEnds(previous *HeapHeaderRoot, rows []RowVersion, xid uint64, end ids.CommitSeq) *HeapHeaderRoot {
	return endHeapHeaders(previous, rows, xid, end, true)
}

// ReplaceHeapHeaders records accepted UPDATE predecessors and successor links.
// The caller owns the successors and captured their headers at actual admission.
func ReplaceHeapHeaders(previous *HeapHeaderRoot, successors []RowVersion, xid uint64) *HeapHeaderRoot {
	var next *HeapHeaderRoot
	for _, successor := range successors {
		if successor.PrivateHistory == nil {
			continue
		}
		row := successor.PrivateHistory.Version
		header := previous.Header(row.Version)
		if next != nil {
			header = next.Header(row.Version)
		}
		if row.RepairStamp != header.RepairStamp {
			continue
		}
		if next == nil {
			next = &HeapHeaderRoot{}
			if previous != nil {
				next.Versions = previous.Versions
				next.Additional, next.Stamp = previous.Additional, previous.Stamp
			}
		}
		header.Xmax, header.Successor = xid, successor.Version
		next.Versions = next.Versions.Set(row.Version, header)
		if header.TupleLockVersion != 0 {
			inherited := next.Header(successor.Version)
			inherited.TupleLockVersion, inherited.TupleLockStamp = header.TupleLockVersion, header.TupleLockStamp
			next.Versions = next.Versions.Set(successor.Version, inherited)
		}
	}
	if next == nil {
		return previous
	}
	return next
}

// Replaces identifies an actual same-header private predecessor, not every
// physical version sharing a logical RowID.
func (v RowVersion) Replaces(target RowVersion) bool {
	if v.Row != target.Row {
		return false
	}
	if v.Version == target.Version && v.RepairStamp == target.RepairStamp {
		return true
	}
	if v.Base == target.Version && v.BaseHeaderStamp() == target.RepairStamp {
		return true
	}
	for prior := v.PrivateHistory; prior != nil; prior = prior.Next {
		if prior.Version.Version == target.Version && prior.Version.RepairStamp == target.RepairStamp {
			return true
		}
	}
	return false
}
