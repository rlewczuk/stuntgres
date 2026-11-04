// Package logical owns the native logical change stream: captured row changes,
// committed history records, positions and the output-plugin seam. It holds no
// lock; the engine runtime owns publication, slots and synchronization.
package logical

import "stuntgres/internal/sqltype"

// ChangeKind is the decoded operation of one Change.
type ChangeKind uint8

const (
	Insert ChangeKind = iota + 1
	Update
	Delete
	Truncate
)

// OldTuple says which old values a change carries, following the relation's
// replica identity when the change was captured.
type OldTuple uint8

const (
	// OldNone: no old values (INSERT, unchanged key, NOTHING, or no key).
	OldNone OldTuple = iota
	// OldKey: Old is full width; only Relation.Key columns are meaningful and
	// every other column is NULL.
	OldKey
	// OldFull: Old holds every column (REPLICA IDENTITY FULL).
	OldFull
)

// Column is one live attribute of a captured relation, resolved when the change
// was captured so later renames, type changes or drops cannot alter it.
type Column struct {
	Name     string
	Number   int32
	Usage    sqltype.Usage
	TypeName string // format_type spelling without typmod, as resolved at capture.
}

// Relation is an immutable relation snapshot shared by the changes of one
// catalog state. Columns are in physical row order.
type Relation struct {
	OID             uint32
	Schema, Name    string
	Columns         []Column
	ReplicaIdentity byte  // 'd', 'n', 'f' or 'i'.
	Key             []int // Column indexes of the replica identity key; empty when none.
}

// Change is one decodable change in transaction order. Value slices are owned
// immutable copies aligned with Relation.Columns.
type Change struct {
	LSN      uint64
	Kind     ChangeKind
	Relation *Relation // nil for Truncate.
	New      []sqltype.Value
	Old      []sqltype.Value
	OldTuple OldTuple
	// Truncate payload: the logged relations and TRUNCATE options.
	Truncated                []*Relation
	Cascade, RestartIdentity bool
}

// approximateSize is the change's in-memory decoding footprint, used only for
// the logical_decoding_work_mem streaming boundary.
func (c *Change) approximateSize() int64 {
	size := int64(80)
	for _, values := range [2][]sqltype.Value{c.New, c.Old} {
		for _, v := range values {
			size += 16
			if text, err := v.Text(); err == nil {
				size += int64(len(text))
			}
		}
	}
	return size
}
