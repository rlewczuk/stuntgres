package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func remapPartitionDescriptor(view catalog.Read, d *catalog.Relation, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, collation collationRemapper) (*catalog.Relation, error) {
	next := *d
	if d.Partition != nil {
		definition := *d.Partition
		definition.Keys = slices.Clone(definition.Keys)
		if !typesOnly {
			definition.Children = slices.Clone(definition.Children)
			for i, address := range definition.Children {
				definition.Children[i] = ref(ids.ObjectRef{Address: address}).Address
			}
			definition.DefaultChild = ref(ids.ObjectRef{Address: definition.DefaultChild}).Address
		}
		for i, key := range definition.Keys {
			key.Usage.Type = typ(key.Usage.Type)
			var err error
			if collation != nil {
				key.Usage.Collation, err = collation(key.Usage.Collation)
				if err != nil {
					return nil, err
				}
				if key.Collation != 0 {
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)}]; ok {
						key.Collation = uint32(target.ObjectID)
					}
				}
			}
			if definition.Strategy == parser.PartitionHash {
				key.Hash, err = sqltype.BindPartitionHash(key.Usage, key.Class)
			} else {
				key.Comparison, err = sqltype.BindIndexOrdering(key.Usage, key.Class)
			}
			if err != nil {
				return nil, err
			}
			if key.Derived != nil {
				derived := *key.Derived
				derived.Definition, _, err = retagBoundTypes(view, derived.Definition, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.Definition, err = remapBoundExpression(derived.Definition, ref, nil)
				if err != nil {
					return nil, err
				}
				derived.Execution, _, err = retagBoundTypes(view, derived.Execution, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.Execution, err = remapBoundExpression(derived.Execution, ref, nil)
				if err != nil {
					return nil, err
				}
				if !typesOnly {
					derived.Dependencies = slices.Clone(derived.Dependencies)
					for j, dependency := range derived.Dependencies {
						derived.Dependencies[j] = ref(dependency)
					}
				}
				key.Derived = &derived
			}
			definition.Keys[i] = key
		}
		next.Partition = &definition
	}
	if d.Membership != nil {
		membership := *d.Membership
		membership.Parent = ref(ids.ObjectRef{Address: membership.Parent}).Address
		membership.Lower, membership.Upper = slices.Clone(membership.Lower), slices.Clone(membership.Upper)
		for _, tuple := range [][]catalog.PartitionDatum{membership.Lower, membership.Upper} {
			for i, datum := range tuple {
				if datum.Kind != 0 {
					continue
				}
				value, err := reownDomainDatum(datum.Value, typ(datum.Value.Type()), collation, view.State().ScalarImports(), view.State().ACLItemImport())
				if err != nil {
					return nil, err
				}
				if !typesOnly && value.Type().ContainsRegclass() {
					value, err = remapRegclassDatum(value, func(id ids.OID) ids.OID {
						if address, ok := mapping[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}]; ok {
							return address.ObjectID
						}
						return id
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && (value.Type().ContainsRegtype() || value.Type().ContainsRegObject()) {
					value, err = remapCatalogAliasDatum(value, func(source ids.ObjectAddress) ids.ObjectAddress {
						if address, ok := mapping[source]; ok {
							return address
						}
						return source
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && value.Type().ContainsRegproc() {
					value, err = remapRoutineDatum(value, func(id ids.OID) ids.OID {
						if address, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
							return address.ObjectID
						}
						return id
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && value.Type().ContainsOperatorAlias() {
					value, err = remapOperatorDatum(value, func(id ids.OID) ids.OID {
						if address, ok := mapping[ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}]; ok {
							return address.ObjectID
						}
						return id
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && value.Type().ContainsRegTS() {
					value, err = remapRegTSDatum(value, func(address ids.ObjectAddress) ids.ObjectAddress {
						if target, ok := mapping[address]; ok {
							return target
						}
						return address
					})
					if err != nil {
						return nil, err
					}
				}
				tuple[i].Value = value
			}
		}
		next.Membership = &membership
	}
	return &next, nil
}
