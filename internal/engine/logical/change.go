// Package logical owns immutable staged changes, transaction lifecycle history,
// positions and the output-plugin seam. It holds no lock; the runtime owns
// actual staging, publication, slots and synchronization.
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
	Rewrite  bool
	// Immutable staging provenance, independent of the surviving projection.
	TopXID, SubXID, Command, Origin uint32
	// Logical decoded budget, not physical WAL or Go heap bytes.
	DecodedBytes int64
	// Truncate payload: the logged relations and TRUNCATE options.
	Truncated                []*Relation
	Cascade, RestartIdentity bool
}

// DecodingSize accounts for the fixed event header, typed value slots and
// canonical payload bytes. Capture measures this once, sharing immutable data.
func (c *Change) DecodingSize() int64 {
	if c.DecodedBytes != 0 {
		return c.DecodedBytes
	}
	size := int64(80)
	for _, values := range [2][]sqltype.Value{c.New, c.Old} {
		for _, v := range values {
			size += v.LogicalSize()
		}
	}
	return size
}
