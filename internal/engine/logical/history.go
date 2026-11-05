package logical

// Positions are pg_lsn values on one cluster-wide stream. Every record (a
// captured change, a commit, a prepare or a prepared-transaction finish)
// occupies one fixed Span, so record boundaries follow from a position alone.
const (
	Base uint64 = 0x01000000
	Span uint64 = 0x40
)

// Allocator hands out record positions. Its zero value starts at Base.
type Allocator struct{ next uint64 }

// Allocate returns the start of a new record.
func (a *Allocator) Allocate() uint64 {
	if a.next == 0 {
		a.next = Base
	}
	lsn := a.next
	a.next += Span
	return lsn
}

// End is the end of the last allocated record: PostgreSQL's flush position.
func (a *Allocator) End() uint64 {
	if a.next == 0 {
		return Base
	}
	return a.next
}

// RecordKind is the transaction event a history record describes.
type RecordKind uint8

const (
	Commit RecordKind = iota + 1
	Prepare
	CommitPrepared
	RollbackPrepared
	RowEvent
	CatalogEvent // Actual catalog/invalidation staging, with no visible tuple.
	Abort
	SubAbort
	SubCommit
	StatementEnd
	StatementAbort
)

// Transaction is the immutable decoded content of one transaction. A prepared
// transaction's Prepare and finish records share it.
type Transaction struct {
	XID         uint32
	FirstLSN    uint64 // First record of the transaction (a change, or its catalog record).
	Changes     []Change
	GID         string
	PrepareLSN  uint64
	PrepareTime int64
}

// Record is one transaction event at LSN. Time is the commit, prepare or
// finish timestamp in PostgreSQL microseconds; Origin is the replication
// origin of the session that ended the transaction (zero for local).
type Record struct {
	Kind   RecordKind
	LSN    uint64
	Time   int64
	Origin uint32
	Txn    *Transaction
	// Child and Parent are canonical low XIDs of the actual child generation.
	// Txn always identifies the top transaction. Change is owned at staging.
	Child, Parent uint32
	Statement     uint32
	Change        *Change
}

// End is the position just after the record.
func (r Record) End() uint64 { return r.LSN + Span }

// History is one database's append-only record log in LSN order. Appends never
// change records a reader already holds, so a Snapshot stays valid after the
// owner's lock is released.
type History struct {
	records []Record
	anchors map[uint32]*Transaction
}

// Append adds a record positioned after every existing one.
func (h *History) Append(r Record) {
	h.records = append(h.records, r)
	if r.Txn != nil && (r.Kind == Commit || r.Kind == Abort || r.Kind == CommitPrepared || r.Kind == RollbackPrepared) {
		delete(h.anchors, r.Txn.XID)
	}
}

// Snapshot returns the current records; callers must not modify them.
func (h *History) Snapshot() []Record { return h.records[:len(h.records):len(h.records)] }

// Anchor returns a shared immutable top-transaction identity. A terminal record
// may add a surviving projection, but never modifies an earlier event's anchor.
func (h *History) Anchor(xid uint32, first uint64) *Transaction {
	if t := h.anchors[xid]; t != nil {
		return t
	}
	t := &Transaction{XID: xid, FirstLSN: first}
	if h.anchors == nil {
		h.anchors = make(map[uint32]*Transaction)
	}
	h.anchors[xid] = t
	return t
}

// Active returns an open/prepared identity without creating an event.
func (h *History) Active(xid uint32) *Transaction { return h.anchors[xid] }

// Len is the number of retained records.
func (h *History) Len() int { return len(h.records) }

// Trim releases a prefix no slot restart or open/prepared anchor needs.
// Allocate a new backing slice: retained snapshots remain immutable.
func (h *History) Trim(keep uint64) {
	i := 0
	for i < len(h.records) && h.records[i].LSN < keep {
		i++
	}
	if i == 0 {
		return
	}
	h.records = append([]Record(nil), h.records[i:]...)
	for xid, t := range h.anchors {
		if t.FirstLSN < keep {
			delete(h.anchors, xid)
		}
	}
}

// Reset drops every record.
func (h *History) Reset() { h.records = nil; h.anchors = nil }

// RestartLSN is the oldest position a slot confirmed at confirmed still needs:
// the first record of any transaction it has yet to receive.
func RestartLSN(records []Record, confirmed uint64) uint64 {
	restart := confirmed
	terminal := make(map[uint32]uint64)
	for _, r := range records {
		if r.Txn != nil && (r.Kind == Commit || r.Kind == Abort || r.Kind == CommitPrepared || r.Kind == RollbackPrepared) {
			terminal[r.Txn.XID] = r.End()
		}
	}
	for _, r := range records {
		if r.Txn == nil {
			continue
		}
		end, done := terminal[r.Txn.XID]
		if (!done || end > confirmed) && r.Txn.FirstLSN < restart {
			restart = r.Txn.FirstLSN
		}
	}
	return restart
}
