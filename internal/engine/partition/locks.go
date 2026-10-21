package partition

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// These newly admitted mutation forms finish ordinary analysis and ACL checks,
// but must not demand defaults, capture virtual storage or publish routed effects.
func WriteExtensionBoundary(statement parser.Statement, d *catalog.Relation) error {
	if d == nil || !catalog.HierarchyRelation(d) {
		return nil
	}
	switch syntax := statement.(type) {
	case *parser.Insert:
		if syntax.Conflict != nil {
			return sqlError("0A000", "ON CONFLICT on partition hierarchies is unsupported")
		}
	case *parser.Merge:
		return sqlError("0A000", "merging partition hierarchies is unsupported")
	}
	return nil
}

func KeyUsesAttribute(d *catalog.Relation, attribute int32) bool {
	if d.Partition == nil {
		return false
	}
	for _, key := range d.Partition.Keys {
		if key.Attribute == attribute {
			return true
		}
		if key.Derived != nil {
			for _, ref := range key.Derived.Dependencies {
				if ref.Address.ClassID == ids.RelationClassID && ref.Address.ObjectID == d.Address().ObjectID && ref.Address.SubID == attribute {
					return true
				}
			}
		}
	}
	return false
}

// The stored defining arrays/key providers remain identical. Only an appended
// child manifest may cross the read admission fence; no name/shape retargeting.
func AppendCompatible(old, current *catalog.Relation) bool {
	if old == nil || current == nil || old.Partition == nil || current.Partition == nil ||
		!catalog.RelationDefiningAuthorityCompatible(old, current) || old.RelationKind != current.RelationKind ||
		old.Membership != current.Membership || old.Inheritance != current.Inheritance ||
		old.Partition.Strategy != current.Partition.Strategy ||
		len(old.Partition.Keys) != len(current.Partition.Keys) ||
		len(current.Partition.Children) <= len(old.Partition.Children) ||
		&old.Partition.Keys[0] != &current.Partition.Keys[0] ||
		!slices.Equal(old.Partition.Children, current.Partition.Children[:len(old.Partition.Children)]) {
		return false
	}
	return old.Partition.DefaultChild == (ids.ObjectAddress{}) || old.Partition.DefaultChild == current.Partition.DefaultChild
}
