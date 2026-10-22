package stored

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func RemapPartitionDescriptor(view catalog.Read, d *catalog.Relation, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, collation typedef.CollationRemapper) (*catalog.Relation, error) {
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
			key.Class, err = routine.BindClassSupport(view, key.Class)
			if err != nil {
				return nil, err
			}
			if definition.Strategy == parser.PartitionHash {
				key.Hash, err = routine.BindPartitionHash(view, key.Usage, key.Class)
			} else {
				key.Comparison, err = routine.BindIndexOrdering(view, key.Usage, key.Class)
			}
			if err != nil {
				return nil, err
			}
			if key.Derived != nil {
				derived := *key.Derived
				derived.Definition, _, err = RetagTypes(view, derived.Definition, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.Definition, err = RemapExpression(derived.Definition, ref, nil)
				if err != nil {
					return nil, err
				}
				derived.Execution, _, err = RetagTypes(view, derived.Execution, typ, collation)
				if err != nil {
					return nil, err
				}
				derived.Execution, err = RemapExpression(derived.Execution, ref, nil)
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
		membership.Values = slices.Clone(membership.Values)
		for _, tuple := range [][]catalog.PartitionDatum{membership.Lower, membership.Upper, membership.Values} {
			for i, datum := range tuple {
				if datum.Kind != 0 {
					continue
				}
				value, err := typedef.ReownDomainDatum(datum.Value, typ(datum.Value.Type()), collation, view.State().ScalarImports(), view.State().ACLItemImport())
				if err != nil {
					return nil, err
				}
				if !typesOnly && value.Type().ContainsRegclass() {
					value, err = regobj.RemapClassDatum(value, func(id ids.OID) ids.OID {
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
					value, err = regobj.RemapAliasDatum(value, func(source ids.ObjectAddress) ids.ObjectAddress {
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
					value, err = regobj.RemapRoutineDatum(value, func(id ids.OID) ids.OID {
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
					value, err = regobj.RemapOperatorDatum(value, func(id ids.OID) ids.OID {
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
					value, err = regobj.RemapTextSearchDatum(value, func(address ids.ObjectAddress) ids.ObjectAddress {
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
