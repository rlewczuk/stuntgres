package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/sqltype"
)

func reownDomainTypes(next *catalogState, mapping map[ObjectAddress]ObjectAddress) (bool, error) {
	visited := make(map[ObjectAddress]bool)
	active := make(map[ObjectAddress]bool)
	changed := false
	collation := remappedCollationCapabilities(next, mapping)
	var visit func(ObjectAddress) error
	visit = func(address ObjectAddress) error {
		if visited[address] {
			return nil
		}
		if active[address] {
			return fmt.Errorf("stuntgres: cyclic domain base")
		}
		d, ok := next.descriptors[address].(*typeDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing domain base")
		}
		if d.typ.IsEnum() {
			target, namespace := address, d.Namespace()
			if destination, ok := mapping[address]; ok {
				target = destination
			}
			if destination, ok := mapping[namespace]; ok {
				namespace = destination
			}
			ns, ok := next.descriptors[d.Namespace()]
			if !ok {
				return fmt.Errorf("stuntgres: missing enum namespace")
			}
			declaration := dependencyIdentifier(ns.Name()) + "." + dependencyIdentifier(d.Name())
			associated := d.array
			if destination, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(associated)}]; ok {
				associated = sqltype.ID(destination.ObjectID)
			}
			if target != address || namespace != d.Namespace() || declaration != d.typ.SQLName() || associated != d.array {
				typ, err := sqltype.EnumWithMembers(sqltype.ID(target.ObjectID), d.Name(), declaration, d.typ.EnumMembers())
				if err != nil {
					return err
				}
				copy := *d
				copy.typ, copy.namespace = typ, namespace
				copy.array = associated
				next.descriptors[address] = &copy
				changed = true
			}
			visited[address] = true
			return nil
		}
		if d.typ.IsComposite() {
			active[address] = true
			relation, ok := next.descriptors[d.owningRelation].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing composite backing relation")
			}
			identity, namespace, owner := address, d.namespace, d.owningRelation
			if to, ok := mapping[identity]; ok {
				identity = to
			}
			if to, ok := mapping[namespace]; ok {
				namespace = to
			}
			if to, ok := mapping[owner]; ok {
				owner = to
			}
			associated := d.array
			if to, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(associated)}]; ok {
				associated = sqltype.ID(to.ObjectID)
			}
			ns := next.descriptors[d.namespace]
			if ns == nil {
				return fmt.Errorf("stuntgres: missing composite namespace")
			}
			declaration := dependencyIdentifier(ns.Name()) + "." + dependencyIdentifier(relation.name)
			rebuild := identity != address || namespace != d.namespace || owner != d.owningRelation || associated != d.array || declaration != d.typ.SQLName() || d.typ.CompositeLen() != len(relation.columns)
			var members []sqltype.CompositeMember
			for i, c := range relation.columns {
				child := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(c.Usage.Type.ID())}
				if err := visit(child); err != nil {
					return err
				}
				usage := c.Usage
				usage.Type = next.descriptors[child].(*typeDescriptor).typ
				if collation != nil {
					mapped, err := collation(usage.Collation)
					if err != nil {
						return err
					}
					usage.Collation = mapped
				}
				member := sqltype.CompositeMember{Attribute: relation.attributes[i], Name: c.Name, Usage: usage}
				if !rebuild && d.typ.CompositeMember(i) != member {
					rebuild = true
				}
				if rebuild && members == nil {
					members = compositeMembers(relation)
					for j := range i {
						members[j].Usage.Type = next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(members[j].Usage.Type.ID())}].(*typeDescriptor).typ
						if collation != nil {
							mapped, err := collation(members[j].Usage.Collation)
							if err != nil {
								return err
							}
							members[j].Usage.Collation = mapped
						}
					}
				}
				if members != nil {
					members[i] = member
				}
			}
			if rebuild {
				if members == nil {
					members = compositeMembers(relation)
				}
				typ, err := sqltype.Composite(sqltype.ID(identity.ObjectID), relation.name, declaration, associated, members)
				if err != nil {
					return err
				}
				copy := *d
				copy.typ, copy.namespace, copy.owningRelation, copy.array = typ, namespace, owner, associated
				next.descriptors[address] = &copy
				changed = true
			}
			delete(active, address)
			visited[address] = true
			return nil
		}
		if d.owningRelation != (ObjectAddress{}) && !d.typ.IsArray() {
			target, namespace, relation := address, d.Namespace(), d.owningRelation
			if to, ok := mapping[address]; ok {
				target = to
			}
			if to, ok := mapping[namespace]; ok {
				namespace = to
			}
			if to, ok := mapping[relation]; ok {
				relation = to
			}
			associated := d.array
			if to, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(associated)}]; ok {
				associated = sqltype.ID(to.ObjectID)
			}
			if target != address || namespace != d.Namespace() || relation != d.owningRelation || associated != d.array {
				copy := *d
				copy.typ = sqltype.LookupOnly(sqltype.ID(target.ObjectID), d.Name(), associated)
				copy.namespace, copy.owningRelation, copy.array = namespace, relation, associated
				next.descriptors[address] = &copy
				changed = true
			}
			visited[address] = true
			return nil
		}
		if !d.typ.IsDomain() && !d.typ.IsArray() {
			visited[address] = true
			return nil
		}
		active[address] = true
		base := d.typ.DomainBase()
		if d.typ.IsArray() {
			base = d.typ.Element().Usage()
		}
		baseAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(base.Type.ID())}
		if err := visit(baseAddress); err != nil {
			return err
		}
		base.Type = next.descriptors[baseAddress].(*typeDescriptor).typ
		collationChanged := false
		if collation != nil {
			mapped, err := collation(base.Collation)
			if err != nil {
				return err
			}
			collationChanged = mapped != base.Collation
			base.Collation = mapped
		}
		target := address
		if destination, ok := mapping[address]; ok {
			target = destination
		}
		namespace := d.Namespace()
		if destination, ok := mapping[namespace]; ok {
			namespace = destination
		}
		ns, ok := next.descriptors[d.Namespace()]
		if !ok {
			return fmt.Errorf("stuntgres: missing domain namespace")
		}
		declaration := dependencyIdentifier(ns.Name()) + "." + dependencyIdentifier(d.Name())
		if d.typ.IsDomain() && !d.typ.Concrete() {
			// Seeded domains carry lookup provenance, not a stored declaration.
			// Preserve their spelling while rebinding the catalog base generation.
			declaration = d.typ.SQLName()
		}
		previous := d.typ.DomainBase().Type
		if d.typ.IsArray() {
			previous = d.typ.Element()
		}
		associated := d.array
		if destination, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(associated)}]; ok {
			associated = sqltype.ID(destination.ObjectID)
		}
		if collationChanged || base.Type != previous || target != address || namespace != d.Namespace() || d.typ.IsDomain() && declaration != d.typ.SQLName() || associated != d.array {
			var typ *sqltype.Descriptor
			var err error
			if d.typ.IsArray() {
				if !d.typ.Concrete() {
					typ = sqltype.LookupArray(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				} else {
					typ, err = sqltype.Array(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				}
			} else if !d.typ.Concrete() {
				typ = sqltype.LookupDomain(sqltype.ID(target.ObjectID), d.Name(), declaration, base)
			} else if d.typ.IsBuiltin() {
				if target != address || associated != d.array {
					return fmt.Errorf("stuntgres: builtin domain identity cannot be remapped")
				}
				typ, err = d.typ.RebindDomain(base, declaration)
			} else {
				typ, err = sqltype.Domain(sqltype.ID(target.ObjectID), d.Name(), declaration, base)
			}
			if err != nil {
				return err
			}
			copy := *d
			copy.typ = typ
			copy.namespace = namespace
			copy.array = associated
			if destination, ok := mapping[copy.owningRelation]; ok {
				copy.owningRelation = destination
			}
			next.descriptors[address] = &copy
			changed = true
		}
		delete(active, address)
		visited[address] = true
		return nil
	}
	for address, d := range next.descriptors {
		if t, ok := d.(*typeDescriptor); ok && (t.typ.IsDomain() || t.typ.IsUserDefined() || t.typ.IsArray() || t.owningRelation != (ObjectAddress{})) {
			if err := visit(address); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

// Only an explicit catalog identity mapping can reidentify a domain. Ordinary
// Value.Retag remains a checked same-logical-ID generation reownership operation.
func reownDomainDatum(value sqltype.Value, target *sqltype.Descriptor, collation collationRemapper, imports map[*sqltype.Descriptor]*sqltype.Descriptor) (sqltype.Value, error) {
	if value.Type().BaseType().IsComposite() && target.BaseType().IsComposite() {
		return value.MapComposite(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return reownDomainDatum(v, t, collation, imports)
		})
	}
	if value.Type().BaseType().IsArray() && target.BaseType().IsArray() {
		return value.MapArray(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return reownDomainDatum(v, t, collation, imports)
		})
	}
	if imported := imports[value.Type().BaseType()]; imported != nil {
		if !target.BaseType().IsEnum() || imported != target.BaseType() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign imported enum destination")
		}
		physical, err := value.BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		physical, err = physical.RemapEnum(target.BaseType())
		if err != nil {
			return sqltype.Value{}, err
		}
		if target.IsDomain() {
			return target.Wrap(physical)
		}
		return physical, nil
	}
	if value.Type().ID() == target.ID() && value.Type().BaseType().ID() == target.BaseType().ID() && (!value.Type().IsDomain() || value.Type().Usage().BaseUsage().Collation == target.Usage().BaseUsage().Collation) {
		return value.Retag(target)
	}
	if !value.Type().IsDomain() || !target.IsDomain() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported scalar identity")
	}
	from, to := value.Type().Usage().BaseUsage(), target.Usage().BaseUsage()
	if collation != nil {
		mapped, err := collation(from.Collation)
		if err != nil {
			return sqltype.Value{}, err
		}
		from.Collation = mapped
	}
	if (!from.Type.IsEnum() && !from.Type.IsComposite() && from.Type.ID() != to.Type.ID()) || from.Typmod != to.Typmod || from.Collation != to.Collation {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported domain base")
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	physical, err = reownDomainDatum(physical, to.Type, collation, imports)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Wrap(physical)
}

func reownCatalogScalarTypes(next *catalogState, storage map[ObjectAddress]*relationStorage, mapping map[ObjectAddress]ObjectAddress) error {
	collation := remappedCollationCapabilities(next, mapping)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if d, ok := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.ID())}].(*typeDescriptor); ok {
			return d.typ
		}
		return nil
	}
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if destination, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.rowType)}]; ok {
			copy := *d
			copy.rowType = sqltype.ID(destination.ObjectID)
			next.descriptors[address] = &copy
			d = &copy
		}
		if destination, ok := mapping[d.ofType]; ok {
			copy := *d
			copy.ofType = destination
			next.descriptors[address] = &copy
			d = &copy
		}
		var columns []ColumnDescriptor
		for i, c := range d.columns {
			target := typ(c.Usage.Type)
			if target == nil {
				return fmt.Errorf("stuntgres: missing imported column type")
			}
			capability := c.Usage.Collation
			if collation != nil {
				var err error
				capability, err = collation(capability)
				if err != nil {
					return err
				}
			}
			if target != c.Usage.Type || capability != c.Usage.Collation {
				if columns == nil {
					columns = append([]ColumnDescriptor(nil), d.columns...)
				}
				columns[i].Usage.Type = target
				columns[i].Usage.Collation = capability
			}
		}
		if columns != nil {
			copy := *d
			copy.columns = columns
			next.descriptors[address] = &copy
		}
	}
	for address, relation := range storage {
		var versions []rowVersion
		for i, row := range relation.versions {
			var values []sqltype.Value
			for j, value := range row.values {
				target := typ(value.Type())
				if target == nil {
					return fmt.Errorf("stuntgres: missing imported datum type")
				}
				rebound := value
				changed := target != value.Type()
				var err error
				if changed {
					rebound, err = reownDomainDatum(value, target, collation, next.enumImports)
					if err != nil {
						return err
					}
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegclass() {
					remapped, er := remapRegclassDatum(rebound, func(id OID) OID {
						if target, ok := mapping[ObjectAddress{ClassID: RelationClassID, ObjectID: id}]; ok {
							return target.ObjectID
						}
						return id
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegtype() {
					remapped, er := remapRegtypeDatum(rebound, func(id OID) OID {
						if target, ok := mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: id}]; ok {
							return target.ObjectID
						}
						return id
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if changed {
					if values == nil {
						values = append([]sqltype.Value(nil), row.values...)
					}
					values[j] = rebound
				}
			}
			if values != nil {
				if versions == nil {
					versions = append([]rowVersion(nil), relation.versions...)
				}
				versions[i].values = values
			}
		}
		if versions != nil {
			copy := *relation
			copy.versions = versions
			storage[address] = &copy
		}
	}
	return nil
}

// Remap captured relation identities recursively without rendering or reparsing
// array text. Payloads are copied only on the first affected element.
func remapRegclassDatum(value sqltype.Value, destination func(OID) OID) (sqltype.Value, error) {
	if value.IsNull() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegclassDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRegclassDatum(element, destination)
		})
	}
	if value.Type().BaseType().ID() != sqltype.Regclass {
		return value, nil
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	text, err := physical.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(OID(oid))
	if target == OID(oid) {
		return value, nil
	}
	physical = sqltype.String(value.Type().BaseType(), strconv.FormatUint(uint64(target), 10))
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
