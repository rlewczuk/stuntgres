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
	Values             []PartitionDatum
}

// PartitionKeyDependsOn reports whether relation's partition key uses target:
// a key type or collation, a key column, or an expression key dependency.
func PartitionKeyDependsOn(relation *Relation, target ids.ObjectAddress) bool {
	if relation.Partition == nil {
		return false
	}
	for _, key := range relation.Partition.Keys {
		if target.ClassID == ids.OperatorClassCatalogID && target.SubID == 0 && target.ObjectID == ids.OID(key.Class.OID()) && target.ObjectID != 0 {
			return true
		}
		if target.ClassID == ids.TypeClassID && ids.OID(key.Usage.Type.ID()) == target.ObjectID {
			return true
		}
		if target.ClassID == ids.CollationClassID && target.SubID == 0 && target.ObjectID != 0 &&
			(ids.OID(key.Usage.Collation.ID) == target.ObjectID || ids.OID(key.Collation) == target.ObjectID) {
			return true
		}
		if key.Attribute != 0 && target.ClassID == ids.RelationClassID && target.ObjectID == relation.Address().ObjectID && target.SubID == key.Attribute {
			return true
		}
		if key.Derived != nil {
			for _, ref := range key.Derived.Dependencies {
				address := ref.Address
				if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
					return true
				}
			}
		}
	}
	return false
}
