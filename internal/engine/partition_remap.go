package engine

import (
	"slices"
	"stuntgres/internal/sqltype"
)

func remapPartitionDescriptor(view CatalogRead, d *relationDescriptor, ref func(ObjectRef) ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, mapping map[ObjectAddress]ObjectAddress, typesOnly bool, collation collationRemapper) (*relationDescriptor, error) {
	next := *d
	if d.partition != nil {
		definition := *d.partition
		definition.keys = slices.Clone(definition.keys)
		if !typesOnly {
			definition.children = slices.Clone(definition.children)
			for i, address := range definition.children {
				definition.children[i] = ref(ObjectRef{Address: address}).Address
			}
			definition.defaultChild = ref(ObjectRef{Address: definition.defaultChild}).Address
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
					if target, ok := mapping[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.collation)}]; ok {
						key.collation = uint32(target.ObjectID)
					}
				}
			}
			key.comparison, err = sqltype.BindIndexOrdering(key.usage, key.class)
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
		next.partition = &definition
	}
	if d.membership != nil {
		membership := *d.membership
		membership.parent = ref(ObjectRef{Address: membership.parent}).Address
		membership.lower, membership.upper = slices.Clone(membership.lower), slices.Clone(membership.upper)
		for _, tuple := range [][]partitionDatum{membership.lower, membership.upper} {
			for i, datum := range tuple {
				if datum.kind != 0 {
					continue
				}
				value, err := reownDomainDatum(datum.value, typ(datum.value.Type()), collation, view.state.enumImports, view.state.aclItemImport)
				if err != nil {
					return nil, err
				}
				if !typesOnly && value.Type().ContainsRegclass() {
					value, err = remapRegclassDatum(value, func(id OID) OID {
						if address, ok := mapping[ObjectAddress{ClassID: RelationClassID, ObjectID: id}]; ok {
							return address.ObjectID
						}
						return id
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && (value.Type().ContainsRegtype() || value.Type().ContainsRegObject()) {
					value, err = remapCatalogAliasDatum(value, func(source ObjectAddress) ObjectAddress {
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
					value, err = remapRoutineDatum(value, func(id OID) OID {
						if address, ok := mapping[ObjectAddress{ClassID: ProcedureClassID, ObjectID: id}]; ok {
							return address.ObjectID
						}
						return id
					})
					if err != nil {
						return nil, err
					}
				}
				if !typesOnly && value.Type().ContainsRegTS() {
					value, err = remapRegTSDatum(value, func(address ObjectAddress) ObjectAddress {
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
		next.membership = &membership
	}
	return &next, nil
}
