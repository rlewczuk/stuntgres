package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type pgDependEdge struct {
	dependent, referenced ObjectAddress
	deptype               byte
}

// Pinning follows represented bootstrap authorities, never an OID cutoff.
func pgDependPinned(catalog CatalogRead, address ObjectAddress) bool {
	if address.ClassID == NamespaceClassID {
		return address.ObjectID == PGCatalogNamespaceOID
	}
	whole := address
	whole.SubID = 0
	d, ok := catalog.Descriptor(whole)
	if !ok {
		return false
	}
	switch d := d.(type) {
	case *typeDescriptor:
		if d.owningRelation != (ObjectAddress{}) {
			relation, ok := catalog.state.descriptors[d.owningRelation].(*relationDescriptor)
			return !ok || relation.relationKind == catalogRelation
		}
		return !d.typ.IsUserDefined() && !(d.typ.IsArray() && d.typ.Element().IsDomain())
	case *collationDescriptor:
		return d.seeded()
	case *conversionDescriptor:
		return true
	case *relationDescriptor:
		return d.relationKind == catalogRelation
	}
	return false
}

func collectPGDepend(ctx context.Context, catalog CatalogRead) ([]pgDependEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var edges []pgDependEdge
	add := func(dependent, referenced ObjectAddress, flavor byte) {
		if referenced.ObjectID != 0 && !pgDependPinned(catalog, referenced) {
			edges = append(edges, pgDependEdge{dependent, referenced, flavor})
		}
	}
	attribute := func(relation ObjectAddress, sub int32) ObjectAddress { relation.SubID = sub; return relation }
	expression := func(owner ObjectAddress, expr BoundExpr, parent ObjectAddress, localFlavor byte) error {
		refs, err := pgDependExpressionRefs(ctx, catalog, expr)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return err
			}
			flavor := byte('n')
			if ref.ClassID == parent.ClassID && ref.ObjectID == parent.ObjectID {
				flavor = localFlavor
			}
			add(owner, ref, flavor)
		}
		return nil
	}
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *extensionDescriptor:
			add(d.address, d.targetNamespace, 'n')
		case *procedureDescriptor:
			add(d.address, d.extension, 'e')
			add(d.address, d.namespace, 'n')
		case *tsTemplateDescriptor:
			add(d.address, d.extension, 'e')
			add(d.address, d.namespace, 'n')
			add(d.address, d.init, 'n')
			add(d.address, d.lexize, 'n')
		case *tsDictionaryDescriptor:
			add(d.address, d.extension, 'e')
			add(d.address, d.namespace, 'n')
			add(d.address, d.template, 'n')
		case *collationDescriptor:
			if !d.seeded() {
				add(d.Address(), d.Namespace(), 'n')
			}
		case *typeDescriptor:
			if pgDependPinned(catalog, d.Address()) {
				continue
			}
			if d.typ.IsArray() {
				add(d.Address(), ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.Element().ID())}, 'i')
			} else if d.owningRelation != (ObjectAddress{}) && !d.typ.IsComposite() {
				add(d.Address(), d.owningRelation, 'i')
			} else if d.typ.IsComposite() && catalog.state.descriptors[d.owningRelation].(*relationDescriptor).relationKind != compositeRelation {
				add(d.Address(), d.owningRelation, 'i')
			} else {
				add(d.Address(), d.Namespace(), 'n')
				if d.typ.IsDomain() {
					add(d.Address(), ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.DomainBase().Type.ID())}, 'n')
					add(d.Address(), ObjectAddress{ClassID: CollationClassID, ObjectID: OID(d.typ.DomainBase().Collation.ID)}, 'n')
				}
			}
		case *relationDescriptor:
			if d.relationKind == catalogRelation {
				continue
			}
			for _, extension := range d.autoExtensions {
				add(d.address, extension, 'x')
			}
			for _, policy := range d.policies {
				owner := ObjectAddress{ClassID: PolicyClassID, ObjectID: policy.OID}
				add(owner, d.address, 'a')
				for _, condition := range []*policyExpression{policy.using, policy.check} {
					if condition != nil {
						if err := expression(owner, condition.expression, d.address, 'n'); err != nil {
							return nil, err
						}
					}
				}
			}
			add(d.address, d.ofType, 'n')
			if d.sqlIndex != nil {
				definition := d.sqlIndex
				if definition.constraint != nil {
					add(d.address, ObjectAddress{ClassID: 2606, ObjectID: definition.constraint.OID}, 'i')
					continue
				}
				// Simple/INCLUDE ownership is a separate pass from expressions.
				attrs := make(map[int32]struct{})
				for _, key := range definition.keys {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if key.derived == nil {
						attrs[key.attribute] = struct{}{}
					} else if column, ok := key.derived.definition.(columnExpression); ok && column.origin.Address.ClassID == definition.parent.ClassID && column.origin.Address.ObjectID == definition.parent.ObjectID {
						// Parentheses around a direct Var do not make an
						// expression-only PostgreSQL index key.
						attrs[column.origin.Address.SubID] = struct{}{}
					}
				}
				for _, sub := range definition.include {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					attrs[sub] = struct{}{}
				}
				if len(attrs) == 0 {
					add(d.address, definition.parent, 'a')
				}
				for sub := range attrs {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(d.address, attribute(definition.parent, sub), 'a')
				}
				// All expression keys share one collection, so repeated inputs
				// across keys do not create repeated dependency records.
				refs := pgDependRefs{ctx: ctx, catalog: catalog}
				for _, key := range definition.keys {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if key.derived != nil {
						refs.expression(key.derived.definition)
					}
					refs.add(ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.collation)})
				}
				addresses, err := refs.finish()
				if err != nil {
					return nil, err
				}
				for _, ref := range addresses {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					flavor := byte('n')
					if ref.ClassID == definition.parent.ClassID && ref.ObjectID == definition.parent.ObjectID {
						flavor = 'a'
						if _, exists := attrs[ref.SubID]; exists {
							continue
						}
					}
					add(d.address, ref, flavor)
				}
				continue
			}
			if d.relationKind == compositeRelation {
				add(d.address, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.rowType)}, 'i')
			} else {
				add(d.address, d.namespace, 'n')
			}
			if d.inheritance != nil {
				for _, parent := range d.inheritance.parents {
					add(d.address, parent, 'n')
				}
			}
			if d.membership != nil {
				add(d.address, d.membership.parent, 'a')
			}
			if d.partition != nil {
				refs := pgDependRefs{ctx: ctx, catalog: catalog}
				for _, key := range d.partition.keys {
					if key.attribute != 0 {
						refs.add(attribute(d.address, key.attribute))
					}
					if key.derived != nil {
						refs.expression(key.derived.definition)
					}
					refs.add(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(key.usage.Type.ID())})
					refs.add(ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.collation)})
					refs.add(ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.usage.Collation.ID)})
				}
				addresses, err := refs.finish()
				if err != nil {
					return nil, err
				}
				for _, referenced := range addresses {
					add(d.address, referenced, 'n')
				}
			}
			for i, column := range d.columns {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := attribute(d.address, d.attributes[i])
				add(owner, ObjectAddress{ClassID: TypeClassID, ObjectID: OID(column.Usage.Type.ID())}, 'n')
				add(owner, ObjectAddress{ClassID: CollationClassID, ObjectID: OID(column.Usage.Collation.ID)}, 'n')
			}
			if d.sequence != nil && d.sequence.owned != (ObjectAddress{}) {
				flavor := byte('a')
				if d.sequence.identity {
					flavor = 'i'
				}
				add(d.address, d.sequence.owned, flavor)
			}
			for _, c := range d.notNullConstraints {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				add(ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.address, c.Attribute), 'a')
			}
			for _, c := range d.uniqueConstraints {
				for _, sub := range c.Attributes {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.address, sub), 'a')
				}
				index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
				if !ok || index.sqlIndex == nil {
					return nil, fmt.Errorf("stuntgres: missing unique dependency index")
				}
				for _, sub := range index.sqlIndex.include {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.address, sub), 'a')
				}
			}
			for _, f := range d.foreignKeys {
				owner := ObjectAddress{ClassID: 2606, ObjectID: f.OID}
				for _, sub := range f.Attributes {
					add(owner, attribute(d.address, sub), 'a')
				}
				for _, sub := range f.Referenced {
					add(owner, attribute(f.Target, sub), 'n')
				}
				add(owner, f.Index, 'n')
			}
			for _, c := range d.exclusionConstraints {
				index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
				if !ok || index.sqlIndex == nil {
					return nil, fmt.Errorf("stuntgres: missing exclusion dependency index")
				}
				owner := ObjectAddress{ClassID: 2606, ObjectID: c.OID}
				for _, key := range index.sqlIndex.keys {
					if key.derived == nil {
						add(owner, attribute(d.address, key.attribute), 'a')
						continue
					}
					refs, err := pgDependExpressionRefs(ctx, catalog, key.derived.definition)
					if err != nil {
						return nil, err
					}
					for _, ref := range refs {
						if ref.ClassID == RelationClassID && ref.ObjectID == d.address.ObjectID && ref.SubID != 0 {
							add(owner, ref, 'a')
						}
						add(owner, ref, 'n')
					}
				}
				for _, sub := range index.sqlIndex.include {
					add(owner, attribute(d.address, sub), 'a')
				}
				if predicate := index.sqlIndex.predicate; predicate != nil {
					refs, err := pgDependExpressionRefs(ctx, catalog, predicate.definition)
					if err != nil {
						return nil, err
					}
					for _, ref := range refs {
						if ref.ClassID == RelationClassID && ref.ObjectID == d.address.ObjectID && ref.SubID != 0 {
							add(owner, ref, 'a')
						}
						add(owner, ref, 'n')
					}
				}
			}
			for _, c := range d.checkConstraints {
				owner := ObjectAddress{ClassID: 2606, ObjectID: c.OID}
				refs, err := pgDependExpressionRefs(ctx, catalog, c.expression)
				if err != nil {
					return nil, err
				}
				columns := false
				for _, ref := range refs {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if ref.ClassID == RelationClassID && ref.ObjectID == d.address.ObjectID && ref.SubID != 0 {
						add(owner, ref, 'a')
						columns = true
					}
					add(owner, ref, 'n')
				}
				if !columns {
					add(owner, d.address, 'a')
				}
			}
			for sub, definition := range d.defaults {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.address, sub), 'a')
				if err := expression(owner, definition.expression, d.address, 'n'); err != nil {
					return nil, err
				}
			}
			for sub, definition := range d.generations {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.address, sub), 'i')
				if err := expression(owner, definition.expression, d.address, 'n'); err != nil {
					return nil, err
				}
			}
			if d.view != nil {
				owner := ObjectAddress{ClassID: 2618, ObjectID: d.view.rule}
				add(owner, d.address, 'i')
				refs, err := pgDependQueryRefs(ctx, catalog, d.view.query)
				if err != nil {
					return nil, err
				}
				for _, ref := range refs {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(owner, ref, 'n')
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}

func capturePGDepend(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	edges, err := collectPGDepend(ctx, catalog)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, len(edges))
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 7)
		for i, n := range [6]int64{int64(edge.dependent.ClassID), int64(edge.dependent.ObjectID), int64(edge.dependent.SubID), int64(edge.referenced.ClassID), int64(edge.referenced.ObjectID), int64(edge.referenced.SubID)} {
			if i == 2 || i == 5 {
				row[i], err = sqltype.Integer(source.columns[i].Usage.Type, n)
			} else {
				row[i], err = sqltype.OIDValue(source.columns[i].Usage.Type, uint32(n))
			}
			if err != nil {
				return nil, err
			}
		}
		row[6] = sqltype.String(source.columns[6].Usage.Type, string(edge.deptype))
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
