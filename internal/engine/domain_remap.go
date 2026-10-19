package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Importing mints destination composite codecs even when logical identities and
// members are unchanged. Visiting their children first then rebuilds dependent
// domains and arrays against those codecs; staged evolution remains copy-on-change.
func reownDomainTypes(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, importing bool) (bool, error) {
	visited := make(map[ids.ObjectAddress]bool)
	active := make(map[ids.ObjectAddress]bool)
	changed := false
	collation := remappedCollationCapabilities(next, mapping)
	var visit func(ids.ObjectAddress) error
	visit = func(address ids.ObjectAddress) error {
		if visited[address] {
			return nil
		}
		if active[address] {
			return fmt.Errorf("stuntgres: cyclic domain base")
		}
		d, ok := next.Type(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing domain base")
		}
		if d.SQLType.IsQueryInt() {
			target, namespace, extension := address, d.Namespace(), d.Extension
			if to, ok := mapping[address]; ok {
				target = to
			}
			if to, ok := mapping[namespace]; ok {
				namespace = to
			}
			if to, ok := mapping[extension]; ok {
				extension = to
			}
			ns, ok := next.Find(d.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing query_int namespace")
			}
			declaration := catalog.DependencyIdentifier(ns.Name()) + "." + catalog.DependencyIdentifier(d.Name())
			associated := d.Array
			if to, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(associated)}]; ok {
				associated = sqltype.ID(to.ObjectID)
			}
			if target != address || namespace != d.Namespace() || extension != d.Extension || associated != d.Array || declaration != d.SQLType.SQLName() {
				typ, err := sqltype.QueryInt(sqltype.ID(target.ObjectID), d.Name(), declaration)
				if err != nil {
					return err
				}
				copy := *d
				copy.SQLType, copy.ObjectNamespace, copy.Extension, copy.Array = typ, namespace, extension, associated
				next.SetDescriptor(address, &copy)
				changed = true
			}
			visited[address] = true
			return nil
		}
		if d.SQLType.IsEnum() {
			target, namespace := address, d.Namespace()
			if destination, ok := mapping[address]; ok {
				target = destination
			}
			if destination, ok := mapping[namespace]; ok {
				namespace = destination
			}
			ns, ok := next.Find(d.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing enum namespace")
			}
			declaration := catalog.DependencyIdentifier(ns.Name()) + "." + catalog.DependencyIdentifier(d.Name())
			associated := d.Array
			if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(associated)}]; ok {
				associated = sqltype.ID(destination.ObjectID)
			}
			if target != address || namespace != d.Namespace() || declaration != d.SQLType.SQLName() || associated != d.Array {
				typ, err := sqltype.EnumWithMembers(sqltype.ID(target.ObjectID), d.Name(), declaration, d.SQLType.EnumMembers())
				if err != nil {
					return err
				}
				copy := *d
				copy.SQLType, copy.ObjectNamespace = typ, namespace
				copy.Array = associated
				next.SetDescriptor(address, &copy)
				changed = true
			}
			visited[address] = true
			return nil
		}
		if d.SQLType.IsComposite() {
			active[address] = true
			relation, ok := next.Relation(d.OwningRelation)
			if !ok {
				return fmt.Errorf("stuntgres: missing composite backing relation")
			}
			identity, namespace, owner := address, d.ObjectNamespace, d.OwningRelation
			if to, ok := mapping[identity]; ok {
				identity = to
			}
			if to, ok := mapping[namespace]; ok {
				namespace = to
			}
			if to, ok := mapping[owner]; ok {
				owner = to
			}
			associated := d.Array
			if to, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(associated)}]; ok {
				associated = sqltype.ID(to.ObjectID)
			}
			ns := next.Get(d.ObjectNamespace)
			if ns == nil {
				return fmt.Errorf("stuntgres: missing composite namespace")
			}
			declaration := catalog.DependencyIdentifier(ns.Name()) + "." + catalog.DependencyIdentifier(relation.Name())
			rebuild := importing || identity != address || namespace != d.ObjectNamespace || owner != d.OwningRelation || associated != d.Array || declaration != d.SQLType.SQLName() || d.SQLType.CompositeLen() != len(relation.Columns)
			var members []sqltype.CompositeMember
			for i, c := range relation.Columns {
				child := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}
				if err := visit(child); err != nil {
					return err
				}
				usage := c.Usage
				usage.Type = next.MustType(child).SQLType
				if collation != nil {
					mapped, err := collation(usage.Collation)
					if err != nil {
						return err
					}
					usage.Collation = mapped
				}
				member := sqltype.CompositeMember{Attribute: relation.Attributes[i], Name: c.Name, Usage: usage}
				if !rebuild && d.SQLType.CompositeMember(i) != member {
					rebuild = true
				}
				if rebuild && members == nil {
					members = compositeMembers(relation)
					for j := range i {
						members[j].Usage.Type = next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(members[j].Usage.Type.ID())}).SQLType
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
				typ, err := sqltype.Composite(sqltype.ID(identity.ObjectID), relation.Name(), declaration, associated, members)
				if err != nil {
					return err
				}
				copy := *d
				copy.SQLType, copy.ObjectNamespace, copy.OwningRelation, copy.Array = typ, namespace, owner, associated
				next.SetDescriptor(address, &copy)
				changed = true
			}
			delete(active, address)
			visited[address] = true
			return nil
		}
		if d.OwningRelation != (ids.ObjectAddress{}) && !d.SQLType.IsArray() {
			target, namespace, relation := address, d.Namespace(), d.OwningRelation
			if to, ok := mapping[address]; ok {
				target = to
			}
			if to, ok := mapping[namespace]; ok {
				namespace = to
			}
			if to, ok := mapping[relation]; ok {
				relation = to
			}
			associated := d.Array
			if to, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(associated)}]; ok {
				associated = sqltype.ID(to.ObjectID)
			}
			if target != address || namespace != d.Namespace() || relation != d.OwningRelation || associated != d.Array {
				copy := *d
				copy.SQLType = sqltype.LookupOnly(sqltype.ID(target.ObjectID), d.Name(), associated)
				copy.ObjectNamespace, copy.OwningRelation, copy.Array = namespace, relation, associated
				next.SetDescriptor(address, &copy)
				changed = true
			}
			visited[address] = true
			return nil
		}
		if !d.SQLType.IsDomain() && !d.SQLType.IsArray() {
			visited[address] = true
			return nil
		}
		active[address] = true
		base := d.SQLType.DomainBase()
		if d.SQLType.IsArray() {
			base = d.SQLType.Element().Usage()
		}
		baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())}
		if err := visit(baseAddress); err != nil {
			return err
		}
		base.Type = next.MustType(baseAddress).SQLType
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
		ns, ok := next.Find(d.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: missing domain namespace")
		}
		declaration := catalog.DependencyIdentifier(ns.Name()) + "." + catalog.DependencyIdentifier(d.Name())
		if d.SQLType.IsDomain() && !d.SQLType.Concrete() {
			// Seeded domains carry lookup provenance, not a stored declaration.
			// Preserve their spelling while rebinding the catalog base generation.
			declaration = d.SQLType.SQLName()
		}
		previous := d.SQLType.DomainBase().Type
		if d.SQLType.IsArray() {
			previous = d.SQLType.Element()
		}
		associated := d.Array
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(associated)}]; ok {
			associated = sqltype.ID(destination.ObjectID)
		}
		if collationChanged || base.Type != previous || target != address || namespace != d.Namespace() || d.SQLType.IsDomain() && declaration != d.SQLType.SQLName() || associated != d.Array {
			var typ *sqltype.Descriptor
			var err error
			if d.SQLType.IsArray() {
				if !d.SQLType.Concrete() {
					typ = sqltype.LookupArray(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				} else {
					typ, err = sqltype.Array(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				}
			} else if !d.SQLType.Concrete() {
				typ = sqltype.LookupDomain(sqltype.ID(target.ObjectID), d.Name(), declaration, base)
			} else if d.SQLType.IsBuiltin() {
				if target != address || associated != d.Array {
					return fmt.Errorf("stuntgres: builtin domain identity cannot be remapped")
				}
				typ, err = d.SQLType.RebindDomain(base, declaration)
			} else {
				typ, err = sqltype.Domain(sqltype.ID(target.ObjectID), d.Name(), declaration, base, len(d.DomainChecks) != 0, d.SQLType.OwnDomainNotNull())
			}
			if err != nil {
				return err
			}
			copy := *d
			copy.SQLType = typ
			copy.ObjectNamespace = namespace
			copy.Array = associated
			if destination, ok := mapping[copy.OwningRelation]; ok {
				copy.OwningRelation = destination
			}
			next.SetDescriptor(address, &copy)
			changed = true
		}
		delete(active, address)
		visited[address] = true
		return nil
	}
	for address, d := range next.Descriptors() {
		if t, ok := d.(*catalog.Type); ok && (t.SQLType.IsDomain() || t.SQLType.IsUserDefined() || t.SQLType.IsArray() || t.OwningRelation != (ids.ObjectAddress{})) {
			if err := visit(address); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

// Only an explicit catalog identity mapping can reidentify a domain. Ordinary
// Value.Retag remains a checked same-logical-ID generation reownership operation.
func reownDomainDatum(value sqltype.Value, target *sqltype.Descriptor, collation collationRemapper, imports map[*sqltype.Descriptor]*sqltype.Descriptor, aclImport func(sqltype.Value) (sqltype.Value, error)) (sqltype.Value, error) {
	if value.Type().BaseType().IsComposite() && target.BaseType().IsComposite() {
		return value.MapComposite(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return reownDomainDatum(v, t, collation, imports, aclImport)
		})
	}
	if value.Type().BaseType().IsArray() && target.BaseType().IsArray() {
		return value.MapArray(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return reownDomainDatum(v, t, collation, imports, aclImport)
		})
	}
	if aclImport != nil && value.Type().ContainsACLItem() {
		var err error
		value, err = aclImport(value)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if imported := imports[value.Type().BaseType()]; imported != nil {
		if imported != target.BaseType() || !target.BaseType().IsEnum() && !target.BaseType().IsQueryInt() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign imported scalar destination")
		}
		physical, err := value.BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		if physical.Type().IsQueryInt() {
			physical, err = physical.RemapQueryInt(target.BaseType())
		} else {
			physical, err = physical.RemapEnum(target.BaseType())
		}
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
	if (!from.Type.IsEnum() && !from.Type.IsComposite() && !from.Type.IsQueryInt() && from.Type.ID() != to.Type.ID()) || from.Typmod != to.Typmod || from.Collation != to.Collation {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported domain base")
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	physical, err = reownDomainDatum(physical, to.Type, collation, imports, aclImport)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Wrap(physical)
}

func reownCatalogScalarTypes(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	collation := remappedCollationCapabilities(next, mapping)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if d, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}); ok {
			return d.SQLType
		}
		return nil
	}
	for address, descriptor := range next.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}]; ok {
			copy := *d
			copy.RowType = sqltype.ID(destination.ObjectID)
			next.SetDescriptor(address, &copy)
			d = &copy
		}
		if destination, ok := mapping[d.OfType]; ok {
			copy := *d
			copy.OfType = destination
			next.SetDescriptor(address, &copy)
			d = &copy
		}
		var columns []catalog.Column
		for i, c := range d.Columns {
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
					columns = append([]catalog.Column(nil), d.Columns...)
				}
				columns[i].Usage.Type = target
				columns[i].Usage.Collation = capability
			}
		}
		if columns != nil {
			copy := *d
			copy.Columns = columns
			next.SetDescriptor(address, &copy)
		}
	}
	for address, relation := range relations {
		var versions []storage.RowVersion
		for i, row := range relation.Versions {
			var values []sqltype.Value
			for j, value := range row.Values {
				target := typ(value.Type())
				if target == nil {
					return fmt.Errorf("stuntgres: missing imported datum type")
				}
				rebound := value
				changed := target != value.Type()
				var err error
				if changed || next.ACLItemImport() != nil && value.Type().ContainsACLItem() {
					rebound, err = reownDomainDatum(value, target, collation, next.ScalarImports(), next.ACLItemImport())
					if err != nil {
						return err
					}
					changed = changed || rebound != value
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegclass() {
					remapped, er := remapRegclassDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}]; ok {
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
				if len(mapping) != 0 && !rebound.IsNull() && (target.ContainsRegtype() || target.ContainsRegObject()) {
					remapped, er := remapCatalogAliasDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
						if target, ok := mapping[address]; ok {
							return target
						}
						return address
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegproc() {
					remapped, er := remapRoutineDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
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
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsOperatorAlias() {
					remapped, er := remapOperatorDatum(rebound, func(id ids.OID) ids.OID {
						if target, ok := mapping[ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}]; ok {
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
				if len(mapping) != 0 && !rebound.IsNull() && target.ContainsRegTS() {
					remapped, er := remapRegTSDatum(rebound, func(address ids.ObjectAddress) ids.ObjectAddress {
						if target, ok := mapping[address]; ok {
							return target
						}
						return address
					})
					if er != nil {
						return er
					}
					changed = changed || remapped != rebound
					rebound = remapped
				}
				if changed {
					if values == nil {
						values = append([]sqltype.Value(nil), row.Values...)
					}
					values[j] = rebound
				}
			}
			if values != nil {
				if versions == nil {
					versions = append([]storage.RowVersion(nil), relation.Versions...)
				}
				versions[i].Values = values
				versions[i].IndexOutputs = nil
			}
		}
		if versions != nil {
			copy := *relation
			copy.Versions = versions
			relations[address] = &copy
		}
	}
	return nil
}

// Remap captured relation identities recursively without rendering or reparsing
// array text. Payloads are copied only on the first affected element.
func remapRegclassDatum(value sqltype.Value, destination func(ids.OID) ids.OID) (sqltype.Value, error) {
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
	target := destination(ids.OID(oid))
	if target == ids.OID(oid) {
		return value, nil
	}
	physical = sqltype.String(value.Type().BaseType(), strconv.FormatUint(uint64(target), 10))
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}
