package sequence

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Columns returns the three columns of a sequence relation, typed from read
// (the engine passes its live catalog read).
func Columns(read catalog.Read) []catalog.Column {
	i, _ := read.LookupType(sqltype.Int8)
	b, _ := read.LookupType(sqltype.Bool)
	return []catalog.Column{{Name: "last_value", Usage: i.Usage(), NullabilityKnown: true}, {Name: "log_cnt", Usage: i.Usage(), NullabilityKnown: true}, {Name: "is_called", Usage: b.Usage(), NullabilityKnown: true}}
}

// RelationSpec is what a new sequence descriptor varies by: CREATE SEQUENCE,
// identity columns and serial columns all build the same shape.
type RelationSpec struct {
	Address, Namespace ids.ObjectAddress
	Name               string
	Revision           ids.DefinitionRevision
	Owner              authorization.PrincipalRef
	Columns            []catalog.Column
	Options            catalog.SequenceOptions
}

// NewRelation builds a sequence descriptor whose plan stamp is its creation
// revision. The descriptor holds its own copy of the options; the caller sets
// the ACL.
func NewRelation(spec RelationSpec) *catalog.Relation {
	options := spec.Options
	return &catalog.Relation{ObjectAddress: spec.Address, ObjectNamespace: spec.Namespace, ObjectName: spec.Name, ObjectRevision: spec.Revision, SequencePlanStamp: spec.Revision, Owner: spec.Owner, Columns: spec.Columns, Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
}

// InitialGenerator is a new sequence's generator: it starts at RESTART when
// given, else at START, in the creating revision's generation.
func InitialGenerator(options catalog.SequenceOptions, restart *int64, revision ids.DefinitionRevision) *storage.SequenceGenerator {
	initial := options.Start
	if restart != nil {
		initial = *restart
	}
	return &storage.SequenceGenerator{Last: initial, Generation: revision}
}

// ReplacementGenerator is the generator an option change publishes: the old
// state in a new generation, restarted (not called) when RESTART is given.
func ReplacementGenerator(state storage.SequenceGenerator, revision ids.DefinitionRevision, restart *int64) *storage.SequenceGenerator {
	replacement := state
	replacement.Generation = revision
	if restart != nil {
		replacement.Last, replacement.Called = *restart, false
	}
	return &replacement
}
