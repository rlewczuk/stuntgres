package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Importing mints destination composite codecs even when logical identities and
// members are unchanged. Visiting their children first then rebuilds dependent
// domains and arrays against those codecs; staged evolution remains copy-on-change.
func ReownDomainTypes(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, importing bool, rangeOrdering func(sqltype.RangeDefinition) (sqltype.RangeDefinition, error)) (bool, error) {
	visited := make(map[ids.ObjectAddress]bool)
	active := make(map[ids.ObjectAddress]bool)
	changed := false
	collation := RemappedCollationCapabilities(next, mapping)
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
		if d.SQLType.IsMultirange() && d.SQLType.IsUserDefined() {
			return visit(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.RangeType().ID())})
		}
		if definition, ok := d.SQLType.RangeDefinition(); ok {
			active[address] = true
			subtypeAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(definition.Subtype.Type.ID())}
			if err := visit(subtypeAddress); err != nil {
				return err
			}
			definition.Subtype.Type = next.MustType(subtypeAddress).SQLType
			if collation != nil {
				var err error
				definition.Subtype.Collation, err = collation(definition.Subtype.Collation)
				if err != nil {
					return err
				}
			}
			if rangeOrdering == nil {
				return fmt.Errorf("stuntgres: missing range support rebinder")
			}
			var err error
			definition, err = rangeOrdering(definition)
			if err != nil {
				return err
			}
			multiAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.MultirangeType().ID())}
			multi, ok := next.Type(multiAddress)
			if !ok {
				return fmt.Errorf("stuntgres: missing multirange import identity")
			}
			destination := func(a ids.ObjectAddress) ids.ObjectAddress {
				if to, ok := mapping[a]; ok {
					return to
				}
				return a
			}
			qualified := func(t *catalog.Type) string {
				return catalog.DependencyIdentifier(next.Get(t.Namespace()).Name()) + "." + catalog.DependencyIdentifier(t.Name())
			}
			oldDefinition, _ := d.SQLType.RangeDefinition()
			if importing || definition != oldDefinition || destination(address) != address || destination(multiAddress) != multiAddress || qualified(d) != d.SQLType.SQLName() || qualified(multi) != multi.SQLType.SQLName() {
				boolean := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)}).SQLType
				r, m, err := sqltype.NewRangeFamily(sqltype.ID(destination(address).ObjectID), d.Name(), qualified(d), sqltype.ID(destination(multiAddress).ObjectID), multi.Name(), qualified(multi), definition, boolean)
				if err != nil {
					return err
				}
				for _, member := range []struct {
					old *catalog.Type
					typ *sqltype.Descriptor
				}{{d, r}, {multi, m}} {
					copy := *member.old
					copy.SQLType = member.typ
					copy.ObjectNamespace = destination(member.old.Namespace())
					copy.Array = sqltype.ID(destination(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(member.old.Array)}).ObjectID)
					next.SetDescriptor(member.old.Address(), &copy)
					if importing {
						if next.ScalarImports() == nil {
							next.SetScalarImports(make(map[*sqltype.Descriptor]*sqltype.Descriptor))
						}
						next.SetScalarImport(member.old.SQLType, member.typ)
					}
				}
				changed = true
			}
			delete(active, address)
			visited[address], visited[multiAddress] = true, true
			return nil
		}
		if d.SQLType.IsHstore() || d.SQLType.IsQueryInt() || d.SQLType.IsLquery() || d.SQLType.IsLtree() || d.SQLType.IsCube() || d.SQLType.IsCitext() || d.SQLType.IsEan13() || d.SQLType.IsIsbn() || d.SQLType.IsIsbn13() || d.SQLType.IsIsmn() || d.SQLType.IsIsmn13() || d.SQLType.IsIssn() || d.SQLType.IsIssn13() || d.SQLType.IsUpc() {
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
				constructor := sqltype.QueryInt
				if d.SQLType.IsLquery() {
					constructor = sqltype.Lquery
				}
				if d.SQLType.IsLtree() {
					constructor = sqltype.Ltree
				}
				if d.SQLType.IsHstore() {
					constructor = sqltype.Hstore
				}
				if d.SQLType.IsCube() {
					constructor = sqltype.Cube
				}
				if d.SQLType.IsCitext() {
					constructor = sqltype.Citext
				}
				if d.SQLType.IsEan13() {
					constructor = sqltype.Ean13
				}
				if d.SQLType.IsIsbn() {
					constructor = sqltype.Isbn
				}
				if d.SQLType.IsIsbn13() {
					constructor = sqltype.Isbn13
				}
				if d.SQLType.IsIsmn() {
					constructor = sqltype.Ismn
				}
				if d.SQLType.IsIsmn13() {
					constructor = sqltype.Ismn13
				}
				if d.SQLType.IsIssn() {
					constructor = sqltype.Issn
				}
				if d.SQLType.IsIssn13() {
					constructor = sqltype.Issn13
				}
				if d.SQLType.IsUpc() {
					constructor = sqltype.Upc
				}
				typ, err := constructor(sqltype.ID(target.ObjectID), d.Name(), declaration)
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
					members = CompositeMembers(relation)
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
					members = CompositeMembers(relation)
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
		if d.SQLType.IsDomain() && d.SQLType.IsBuiltin() && !d.SQLType.Concrete() {
			// Seeded domains carry builtin lookup provenance, not a stored
			// declaration. User lookup-only domains still use their current name.
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
			if d.SQLType.IsCatalogVector() {
				if target != address || namespace != d.Namespace() || associated != d.Array {
					return fmt.Errorf("stuntgres: builtin vector identity cannot be remapped")
				}
				typ, err = d.SQLType.RebindCatalogVector(base.Type)
			} else if d.SQLType.IsArray() {
				if !d.SQLType.Concrete() {
					typ = sqltype.LookupArray(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				} else {
					typ, err = sqltype.Array(sqltype.ID(target.ObjectID), d.Name(), base.Type)
				}
			} else if !d.SQLType.Concrete() {
				typ = sqltype.LookupDomain(sqltype.ID(target.ObjectID), d.Name(), declaration, base, len(d.DomainChecks) != 0, d.SQLType.OwnDomainNotNull())
			} else if d.SQLType.IsBuiltin() {
				if target != address || associated != d.Array {
					return fmt.Errorf("stuntgres: builtin domain identity cannot be remapped")
				}
				typ, err = d.SQLType.RebindDomain(base, d.Name(), declaration)
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
func ReownDomainDatum(value sqltype.Value, target *sqltype.Descriptor, collation CollationRemapper, imports map[*sqltype.Descriptor]*sqltype.Descriptor, aclImport func(sqltype.Value) (sqltype.Value, error)) (sqltype.Value, error) {
	if source := value.Type().BaseType().RangeType(); source != nil {
		if _, custom := source.RangeDefinition(); custom {
			physicalTarget := target.BaseType()
			if value.Type().BaseType().ID() != physicalTarget.ID() && imports[value.Type().BaseType()] != physicalTarget {
				return sqltype.Value{}, fmt.Errorf("stuntgres: unauthorized imported range identity")
			}
			mode := sqltype.RangeMapGeneration
			if len(imports) != 0 {
				mode = sqltype.RangeMapIdentity
			}
			return value.MapRange(target, mode, func(endpoint sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
				return ReownDomainDatum(endpoint, t, collation, imports, aclImport)
			})
		}
	}
	if value.Type().BaseType().IsComposite() && target.BaseType().IsComposite() {
		return value.MapComposite(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return ReownDomainDatum(v, t, collation, imports, aclImport)
		})
	}
	if value.Type().BaseType().IsArray() && target.BaseType().IsArray() {
		return value.MapArray(target, func(v sqltype.Value, t *sqltype.Descriptor) (sqltype.Value, error) {
			return ReownDomainDatum(v, t, collation, imports, aclImport)
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
		if imported != target.BaseType() || !target.BaseType().IsEnum() && !target.BaseType().IsHstore() && !target.BaseType().IsQueryInt() && !target.BaseType().IsLquery() && !target.BaseType().IsLtree() && !target.BaseType().IsCube() && !target.BaseType().IsCitext() && !target.BaseType().IsEan13() && !target.BaseType().IsIsbn() && !target.BaseType().IsIsbn13() && !target.BaseType().IsIsmn() && !target.BaseType().IsIsmn13() && !target.BaseType().IsIssn() && !target.BaseType().IsIssn13() && !target.BaseType().IsUpc() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign imported scalar destination")
		}
		physical, err := value.BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		if physical.Type().IsHstore() {
			physical, err = physical.RemapHstore(target.BaseType())
		} else if physical.Type().IsQueryInt() {
			physical, err = physical.RemapQueryInt(target.BaseType())
		} else if physical.Type().IsLquery() {
			physical, err = physical.RemapLquery(target.BaseType())
		} else if physical.Type().IsLtree() {
			physical, err = physical.RemapLtree(target.BaseType())
		} else if physical.Type().IsCube() {
			physical, err = physical.RemapCube(target.BaseType())
		} else if physical.Type().IsCitext() {
			physical, err = physical.RemapCitext(target.BaseType())
		} else if physical.Type().IsEan13() {
			physical, err = physical.RemapEan13(target.BaseType())
		} else if physical.Type().IsIsbn() {
			physical, err = physical.RemapIsbn(target.BaseType())
		} else if physical.Type().IsIsbn13() {
			physical, err = physical.RemapIsbn13(target.BaseType())
		} else if physical.Type().IsIsmn() {
			physical, err = physical.RemapIsmn(target.BaseType())
		} else if physical.Type().IsIsmn13() {
			physical, err = physical.RemapIsmn13(target.BaseType())
		} else if physical.Type().IsIssn() {
			physical, err = physical.RemapIssn(target.BaseType())
		} else if physical.Type().IsIssn13() {
			physical, err = physical.RemapIssn13(target.BaseType())
		} else if physical.Type().IsUpc() {
			physical, err = physical.RemapUpc(target.BaseType())
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
	if (!from.Type.IsEnum() && !from.Type.IsComposite() && !from.Type.IsHstore() && !from.Type.IsQueryInt() && (!from.Type.IsLquery() && !from.Type.IsLtree()) && !from.Type.IsCube() && !from.Type.IsCitext() && !from.Type.IsEan13() && !from.Type.IsIsbn() && !from.Type.IsIsbn13() && !from.Type.IsIsmn() && !from.Type.IsIsmn13() && !from.Type.IsIssn() && !from.Type.IsIssn13() && !from.Type.IsUpc() && from.Type.ID() != to.Type.ID()) || from.Typmod != to.Typmod || from.Collation != to.Collation {
		return sqltype.Value{}, fmt.Errorf("stuntgres: incompatible imported domain base")
	}
	physical, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	physical, err = ReownDomainDatum(physical, to.Type, collation, imports, aclImport)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Wrap(physical)
}
