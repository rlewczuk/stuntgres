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
}

// End is the position just after the record.
func (r Record) End() uint64 { return r.LSN + Span }

// History is one database's append-only record log in LSN order. Appends never
// change records a reader already holds, so a Snapshot stays valid after the
// owner's lock is released.
type History struct{ records []Record }

// Append adds a record positioned after every existing one.
func (h *History) Append(r Record) { h.records = append(h.records, r) }

// Snapshot returns the current records; callers must not modify them.
func (h *History) Snapshot() []Record { return h.records[:len(h.records):len(h.records)] }

// Len is the number of retained records.
func (h *History) Len() int { return len(h.records) }

// Trim releases records positioned before keep, the lowest confirmed position
// of the database's slots: no slot can emit them again. A prepared
// transaction's finish record carries its content, so prepare records need no
// longer retention.
func (h *History) Trim(keep uint64) {
	i := 0
	for i < len(h.records) && h.records[i].LSN < keep {
		i++
	}
	if i == 0 {
		return
	}
	h.records = append([]Record(nil), h.records[i:]...)
}

// Reset drops every record.
func (h *History) Reset() { h.records = nil }

// RestartLSN is the oldest position a slot confirmed at confirmed still needs:
// the first record of any transaction it has yet to receive.
func RestartLSN(records []Record, confirmed uint64) uint64 {
	restart := confirmed
	for _, r := range records {
		if r.LSN >= confirmed && r.Txn != nil && r.Txn.FirstLSN < restart {
			restart = r.Txn.FirstLSN
		}
	}
	return restart
}
