package control

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/table"
)

// Schema is one ordinary namespace Dump creates.
type Schema struct {
	Name, Owner string
	Configured  bool
}

// OwnedStatement is one owned definition statement: a view, extension or
// index.
type OwnedStatement struct {
	Owner, SQL string
}

// SchemaComment is a schema comment that differs from its baseline.
type SchemaComment struct{ Name, Text string }

// Capture is everything Dump reads from the engine. The root fills it under
// Engine.mu and hands it over: after the unlock Dump reads only the capture,
// sorting and completing it in place, and the root never reads it again.
type Capture struct {
	// CatalogView binds private key names until the terminal renames;
	// OriginalCatalog keeps the captured names for later definitions and
	// values.
	CatalogView, OriginalCatalog catalog.Read
	OriginalRoles, DomainRoles   authorization.Read
	ConstraintReplay             *partition.ConstraintReplay
	ACLOutput                    regobj.ACLItemOutput

	// Preamble reconstructs bootstrap identity changes before ordinary
	// namespaces are created.
	Preamble       []string
	Schemas        []Schema
	SchemaComments []SchemaComment
	Sequences      []sequence.Dump
	Domains        []*catalog.Type
	Collations     []*catalog.Collation
	Conversions    []*catalog.Conversion
	Operators      []*catalog.Operator
	SQLRoutines    []*catalog.Procedure
	Tables         []table.DumpRelation
	Materialized   []table.DumpRelation
	Views          []OwnedStatement
	Indexes        []OwnedStatement
	ViewIndices    map[ids.OID]int
	TableIndices   map[ids.OID]int
	IndexIndices   map[ids.OID]int
	Composites     map[ids.OID]*catalog.Relation
	Graph          dump.Graph
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	InlinePredecessors map[ids.OID][]ids.OID
	DeferDefaults      bool
}
