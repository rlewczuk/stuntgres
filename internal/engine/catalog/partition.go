package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type PartitionKeyEntry struct {
	Attribute     int32
	Derived       *DerivedIndexKey
	Usage         sqltype.Usage
	Class         sqltype.IndexClass
	Comparison    sqltype.IndexComparison
	Hash          sqltype.PartitionHash
	Collation     uint32
	ExplicitClass bool
	Label         string
}

type PartitionDefinition struct {
	Strategy     parser.PartitionStrategy
	Keys         []PartitionKeyEntry
	Children     []ids.ObjectAddress
	DefaultChild ids.ObjectAddress
}

type PartitionDatum struct {
	Kind  int8
	Value sqltype.Value
	Pos   parser.Position
}

type PartitionMembership struct {
	Parent             ids.ObjectAddress
	Strategy           parser.PartitionStrategy
	Modulus, Remainder int32
	DefaultBound       bool
	Lower, Upper       []PartitionDatum
}
