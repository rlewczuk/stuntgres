package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

// remapReplacementRelation returns relation r moved to target with its
// inheritance, constraint, index, sequence and identity references moved
// through mapping. The exact immutable catalog provider certified for
// retained TEMP consumers stays as it is unless its namespace moved.
func remapReplacementRelation(current catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, a, target ids.ObjectAddress, r *catalog.Relation) (catalog.Descriptor, error) {
	if target == a && r.RelationKind == catalog.CatalogRelation && catalog.Descriptor(r) == current.State().Get(a) && stored.DependPinned(current, nil, a) {
		if _, moved := mapping[r.Namespace()]; !moved {
			// Remapping unrelated imported identities must not copy
			// the exact provider certified for retained TEMP consumers.
			return r, nil
		}
	}
	n := *r
	n.ObjectAddress = target
	if r.Inheritance != nil {
		n.Inheritance = table.RemapInheritance(r.Inheritance, func(address ids.ObjectAddress) ids.ObjectAddress {
			if destination, ok := mapping[address]; ok {
				return destination
			}
			return address
		}, func(origin catalog.InheritanceOrigin) ids.OID { return origin.Constraint })
	}
	table.RemapPartitionConstraintOrigins(&n, func(address ids.ObjectAddress) ids.ObjectAddress {
		if destination, ok := mapping[address]; ok {
			return destination
		}
		return address
	}, func(origin catalog.InheritanceOrigin) ids.OID { return origin.Constraint })
	if target != a {
		n.Columns = append([]catalog.Column(nil), r.Columns...)
		for i := range n.Columns {
			if n.Columns[i].OriginTable == uint32(a.ObjectID) {
				n.Columns[i].OriginTable = uint32(target.ObjectID)
			}
		}
	}
	if dest, ok := mapping[n.Namespace()]; ok {
		n.ObjectNamespace = dest
	}
	if len(r.Constraints.Unique) != 0 {
		n.Constraints.Unique = append([]catalog.UniqueConstraint(nil), r.Constraints.Unique...)
		for i := range n.Constraints.Unique {
			if dest, ok := mapping[n.Constraints.Unique[i].Index]; ok {
				n.Constraints.Unique[i].Index = dest
			}
		}
	}
	if err := table.RemapPhysicalNotNullReferences(r, &n, mapping); err != nil {
		return nil, err
	}
	if len(r.Constraints.Exclusion) != 0 {
		n.Constraints.Exclusion = append([]catalog.ExclusionConstraint(nil), r.Constraints.Exclusion...)
		for i := range n.Constraints.Exclusion {
			if dest, ok := mapping[n.Constraints.Exclusion[i].Index]; ok {
				n.Constraints.Exclusion[i].Index = dest
			}
		}
	}
	if len(r.Constraints.ForeignKeys) != 0 {
		n.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), r.Constraints.ForeignKeys...)
		for i := range n.Constraints.ForeignKeys {
			if dest, ok := mapping[n.Constraints.ForeignKeys[i].Target]; ok {
				n.Constraints.ForeignKeys[i].Target = dest
			}
			if dest, ok := mapping[n.Constraints.ForeignKeys[i].Index]; ok {
				n.Constraints.ForeignKeys[i].Index = dest
			}
		}
	}
	if r.SQLIndex != nil {
		definition := *r.SQLIndex
		if parent, remapped := mapping[definition.PartitionParent]; remapped {
			definition.PartitionParent = parent
		}
		if parent, remapped := mapping[definition.Parent]; remapped {
			definition.Parent = parent
		}
		if definition.Constraint != nil {
			constraint := *definition.Constraint
			if dest, remapped := mapping[constraint.Index]; remapped {
				constraint.Index = dest
			}
			definition.Constraint = &constraint
		}
		n.SQLIndex = &definition
	}
	if r.Sequence != nil {
		o := *r.Sequence
		whole := o.Owned
		whole.SubID = 0
		if dest, ok := mapping[whole]; ok {
			dest.SubID = o.Owned.SubID
			o.Owned = dest
		}
		n.Sequence = &o
	}
	if len(r.ColumnMeta.Identities) != 0 {
		n.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity, len(r.ColumnMeta.Identities))
		for attribute, identity := range r.ColumnMeta.Identities {
			if destination, ok := mapping[identity.Sequence]; ok {
				identity.Sequence = destination
			}
			n.ColumnMeta.Identities[attribute] = identity
		}
	}
	return &n, nil
}
