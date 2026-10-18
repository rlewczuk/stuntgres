package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func remapPartitionDescriptor(view CatalogRead, d *relationDescriptor, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, collation collationRemapper) (*relationDescriptor, error) {
	next := *d
	if d.Partition != nil {
		definition := *d.Partition
		definition.keys = slices.Clone(definition.keys)
		if !typesOnly {
			definition.children = slices.Clone(definition.children)
			for i, address := range definition.children {
				definition.children[i] = ref(ids.ObjectRef{Address: address}).Address
			}
			definition.defaultChild = ref(ids.ObjectRef{Address: definition.defaultChild}).Address
		}
		for i, key := range definition.keys {
			key.usage.Type = typ(key.usage.Type)
			var err error
			if collation != nil {
				key.usage.Collation, err = collation(key.usage.Collation)
				if err != nil {
					return nil, err
				}
				if key.collation != 0 {
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.collation)}]; ok {
						key.collation = uint32(target.ObjectID)
					}
				}
			}
			if definition.strategy == parser.PartitionHash {
				key.hash, err = sqltype.BindPartitionHash(key.usage, key.class)
			} else {
				key.comparison, err = sqltype.BindIndexOrdering(key.usage, key.class)
			}
			if err != nil {
				return nil, err
			}
			if key.derived != nil {
				derived := *key.derived
				derived.definition, _, err = retagBoundTypes(view, derived.definition, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.definition, err = remapBoundExpression(derived.definition, ref, nil)
				if err != nil {
					return nil, err
				}
				derived.execution, _, err = retagBoundTypes(view, derived.execution, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.execution, err = remapBoundExpression(derived.execution, ref, nil)
				if err != nil {
					return nil, err
				}
				if !typesOnly {
					derived.dependencies = slices.Clone(derived.dependencies)
					for j, dependency := range derived.dependencies {
						derived.dependencies[j] = ref(dependency)
					}
				}
				key.derived = &derived
			}
			definition.keys[i] = key
		}
		next.Partition = &definition
	}
	if d.Membership != nil {
		membership := *d.Membership
		membership.parent = ref(ids.ObjectRef{Address: membership.parent}).Address
		membership.lower, membership.upper = slices.Clone(membership.lower), slices.Clone(membership.upper)
		for _, tuple := range [][]partitionDatum{membership.lower, membership.upper} {
			for i, datum := range tuple {
				if datum.kind != 0 {
					continue
				}
				value, err := reownDomainDatum(datum.value, typ(datum.value.Type()), collation, view.state.scalarImports, view.state.aclItemImport)
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
				tuple[i].value = value
			}
		}
		next.Membership = &membership
	}
	return &next, nil
}
