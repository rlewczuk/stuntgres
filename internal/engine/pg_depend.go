package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type pgDependEdge struct {
	dependent, referenced ids.ObjectAddress
	deptype               byte
}

// Pinning follows represented bootstrap authorities, never an OID cutoff.
func pgDependPinned(view CatalogRead, address ids.ObjectAddress) bool {
	if address.ClassID == ids.NamespaceClassID {
		return address.ObjectID == ids.PGCatalogNamespaceOID || address.ObjectID == ids.PGToastNamespaceOID
	}
	whole := address
	whole.SubID = 0
	d, ok := view.Descriptor(whole)
	if !ok {
		return false
	}
	switch d := d.(type) {
	case *typeDescriptor:
		if d.OwningRelation != (ids.ObjectAddress{}) {
			relation, ok := view.Relation(d.OwningRelation)
			return !ok || relation.RelationKind == catalog.CatalogRelation
		}
		// Lookup-only array codecs do not imply bootstrap lifetime. Automatic
		// arrays retain their owned element's dependency even without storage.
		return !d.SQLType.IsUserDefined() && !(d.SQLType.IsArray() && (d.SQLType.Element().IsDomain() || d.SQLType.Element().IsUserDefined()))
	case *collationDescriptor:
		return d.Seeded()
	case *conversionDescriptor:
		return d.Seeded()
	case *catalog.Procedure:
		return d.Seeded()
	case *catalog.Operator:
		return operatorSeeded(d)
	case *catalog.Relation:
		return d.RelationKind == catalog.CatalogRelation
	}
	return false
}

func collectPGDepend(ctx context.Context, view CatalogRead) ([]pgDependEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var edges []pgDependEdge
	add := func(dependent, referenced ids.ObjectAddress, flavor byte) {
		if referenced.ObjectID != 0 && !pgDependPinned(view, referenced) {
			edges = append(edges, pgDependEdge{dependent, referenced, flavor})
		}
	}
	attribute := func(relation ids.ObjectAddress, sub int32) ids.ObjectAddress { relation.SubID = sub; return relation }
	expression := func(owner ids.ObjectAddress, expr expr.Expr, parent ids.ObjectAddress, localFlavor byte) error {
		refs, err := pgDependExpressionRefs(ctx, view, expr)
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
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Operator:
			if !operatorSeeded(d) {
				for _, address := range operatorDependencyAddresses(view, d) {
					add(d.Address(), address, 'n')
				}
			}
		case *foreignDescriptor:
			for _, address := range foreignDependencyAddresses(d) {
				if address != (ids.ObjectAddress{}) {
					add(d.Address(), address, 'n')
				}
			}
		case *extensionDescriptor:
			add(d.Address(), d.TargetNamespace, 'n')
		case *catalog.Procedure:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			if intarrayProviderOf(d) != nil {
				if d.Name() == "bqarr_in" {
					add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}, 'n')
				} else {
					ext := view.MustExtension(d.Extension)
					add(d.Address(), ext.Members[0], 'n')
				}
			}
		case *tsTemplateDescriptor:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			add(d.Address(), d.Init, 'n')
			add(d.Address(), d.Lexize, 'n')
		case *tsDictionaryDescriptor:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			add(d.Address(), d.Template, 'n')
		case *collationDescriptor:
			if !d.Seeded() {
				add(d.Address(), d.Namespace(), 'n')
			}
		case *conversionDescriptor:
			if !d.Seeded() {
				add(d.Address(), d.Namespace(), 'n')
				add(d.Address(), d.Handler, 'n')
			}
		case *typeDescriptor:
			if pgDependPinned(view, d.Address()) {
				continue
			}
			add(d.Address(), d.Extension, 'e')
			if d.SQLType.IsArray() {
				add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())}, 'i')
			} else if d.OwningRelation != (ids.ObjectAddress{}) && !d.SQLType.IsComposite() {
				add(d.Address(), d.OwningRelation, 'i')
			} else if d.SQLType.IsComposite() && view.MustRelation(d.OwningRelation).RelationKind != catalog.CompositeRelation {
				add(d.Address(), d.OwningRelation, 'i')
			} else {
				add(d.Address(), d.Namespace(), 'n')
				if d.SQLType.IsDomain() {
					add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Type.ID())}, 'n')
					add(d.Address(), ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Collation.ID)}, 'n')
				}
			}
		case *catalog.Relation:
			if d.Foreign != nil {
				add(d.Address(), d.Foreign.Server, 'n')
			}
			if d.RelationKind == catalog.CatalogRelation {
				continue
			}
			for _, extension := range d.AutoExtensions {
				add(d.Address(), extension, 'x')
			}
			for _, policy := range d.Policies {
				owner := ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}
				add(owner, d.Address(), 'a')
				for _, condition := range []*policyExpression{policyExpressionOf(policy.Using), policyExpressionOf(policy.Check)} {
					if condition != nil {
						if err := expression(owner, condition.expression, d.Address(), 'n'); err != nil {
							return nil, err
						}
					}
				}
			}
			add(d.Address(), d.OfType, 'n')
			if d.SQLIndex != nil {
				definition := d.SQLIndex
				if definition.PartitionParent != (ids.ObjectAddress{}) {
					add(d.Address(), definition.PartitionParent, 'P')
					add(d.Address(), definition.Parent, 'S')
				}
				if definition.Constraint != nil {
					add(d.Address(), ids.ObjectAddress{ClassID: 2606, ObjectID: definition.Constraint.OID}, 'i')
					continue
				}
				// Simple/INCLUDE ownership is a separate pass from expressions.
				attrs := make(map[int32]struct{})
				for _, key := range definition.Keys {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if key.Derived == nil {
						attrs[key.Attribute] = struct{}{}
					} else if column, ok := key.Derived.Definition.(columnExpression); ok && column.origin.Address.ClassID == definition.Parent.ClassID && column.origin.Address.ObjectID == definition.Parent.ObjectID {
						// Parentheses around a direct Var do not make an
						// expression-only PostgreSQL index key.
						attrs[column.origin.Address.SubID] = struct{}{}
					}
				}
				for _, sub := range definition.Include {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					attrs[sub] = struct{}{}
				}
				if len(attrs) == 0 {
					add(d.Address(), definition.Parent, 'a')
				}
				for sub := range attrs {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(d.Address(), attribute(definition.Parent, sub), 'a')
				}
				// All expression keys share one collection, so repeated inputs
				// across keys do not create repeated dependency records.
				refs := pgDependRefs{ctx: ctx, catalog: view}
				for _, key := range definition.Keys {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if key.Derived != nil {
						refs.expression(key.Derived.Definition)
					}
					refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
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
					if ref.ClassID == definition.Parent.ClassID && ref.ObjectID == definition.Parent.ObjectID {
						flavor = 'a'
						if _, exists := attrs[ref.SubID]; exists {
							continue
						}
					}
					add(d.Address(), ref, flavor)
				}
				continue
			}
			if d.RelationKind == catalog.CompositeRelation {
				add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}, 'i')
			} else {
				add(d.Address(), d.Namespace(), 'n')
			}
			if d.Inheritance != nil {
				for _, parent := range d.Inheritance.Parents {
					add(d.Address(), parent, 'n')
				}
			}
			if d.Membership != nil {
				add(d.Address(), d.Membership.Parent, 'a')
			}
			if d.Partition != nil {
				refs := pgDependRefs{ctx: ctx, catalog: view}
				for _, key := range d.Partition.Keys {
					if key.Attribute != 0 {
						refs.add(attribute(d.Address(), key.Attribute))
					}
					if key.Derived != nil {
						refs.expression(key.Derived.Definition)
					}
					refs.add(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(key.Usage.Type.ID())})
					refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
					refs.add(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Usage.Collation.ID)})
				}
				addresses, err := refs.finish()
				if err != nil {
					return nil, err
				}
				for _, referenced := range addresses {
					add(d.Address(), referenced, 'n')
				}
			}
			for i, column := range d.Columns {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := attribute(d.Address(), d.Attributes[i])
				add(owner, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())}, 'n')
				add(owner, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(column.Usage.Collation.ID)}, 'n')
			}
			if d.Sequence != nil && d.Sequence.Owned != (ids.ObjectAddress{}) {
				flavor := byte('a')
				if d.Sequence.Identity {
					flavor = 'i'
				}
				add(d.Address(), d.Sequence.Owned, flavor)
			}
			for _, c := range d.NotNullConstraints {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.Address(), c.Attribute), 'a')
			}
			for _, c := range d.UniqueConstraints {
				if c.Parent != 0 {
					add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, ids.ObjectAddress{ClassID: 2606, ObjectID: c.Parent}, 'P')
					add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, d.Address(), 'S')
				}
				for _, sub := range c.Attributes {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.Address(), sub), 'a')
				}
				index, ok := view.Relation(c.Index)
				if !ok || index.SQLIndex == nil {
					return nil, fmt.Errorf("stuntgres: missing unique dependency index")
				}
				for _, sub := range index.SQLIndex.Include {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.Address(), sub), 'a')
				}
			}
			for _, f := range d.ForeignKeys {
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: f.OID}
				for _, sub := range f.Attributes {
					add(owner, attribute(d.Address(), sub), 'a')
				}
				for _, sub := range f.Referenced {
					add(owner, attribute(f.Target, sub), 'n')
				}
				add(owner, f.Index, 'n')
			}
			for _, c := range d.ExclusionConstraints {
				index, ok := view.Relation(c.Index)
				if !ok || index.SQLIndex == nil {
					return nil, fmt.Errorf("stuntgres: missing exclusion dependency index")
				}
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}
				for _, key := range index.SQLIndex.Keys {
					if key.Derived == nil {
						add(owner, attribute(d.Address(), key.Attribute), 'a')
						continue
					}
					refs, err := pgDependExpressionRefs(ctx, view, key.Derived.Definition)
					if err != nil {
						return nil, err
					}
					for _, ref := range refs {
						if ref.ClassID == ids.RelationClassID && ref.ObjectID == d.Address().ObjectID && ref.SubID != 0 {
							add(owner, ref, 'a')
						}
						add(owner, ref, 'n')
					}
				}
				for _, sub := range index.SQLIndex.Include {
					add(owner, attribute(d.Address(), sub), 'a')
				}
				if predicate := index.SQLIndex.Predicate; predicate != nil {
					refs, err := pgDependExpressionRefs(ctx, view, predicate.Definition)
					if err != nil {
						return nil, err
					}
					for _, ref := range refs {
						if ref.ClassID == ids.RelationClassID && ref.ObjectID == d.Address().ObjectID && ref.SubID != 0 {
							add(owner, ref, 'a')
						}
						add(owner, ref, 'n')
					}
				}
			}
			for _, c := range d.CheckConstraints {
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}
				refs, err := pgDependExpressionRefs(ctx, view, c.Expression)
				if err != nil {
					return nil, err
				}
				columns := false
				for _, ref := range refs {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if ref.ClassID == ids.RelationClassID && ref.ObjectID == d.Address().ObjectID && ref.SubID != 0 {
						add(owner, ref, 'a')
						columns = true
					}
					add(owner, ref, 'n')
				}
				if !columns {
					add(owner, d.Address(), 'a')
				}
			}
			for sub, definition := range d.Defaults {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.Address(), sub), 'a')
				if err := expression(owner, definition.Expression, d.Address(), 'n'); err != nil {
					return nil, err
				}
			}
			for sub, definition := range d.Generations {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.Address(), sub), 'i')
				if err := expression(owner, definition.Expression, d.Address(), 'n'); err != nil {
					return nil, err
				}
			}
			for _, rule := range rulesOf(d) {
				owner := ids.ObjectAddress{ClassID: 2618, ObjectID: rule.OID}
				add(owner, d.Address(), 'a')
				for _, ref := range rule.references {
					add(owner, ref, 'n')
				}
			}
			if d.View != nil {
				owner := ids.ObjectAddress{ClassID: 2618, ObjectID: d.View.RuleOID()}
				add(owner, d.Address(), 'i')
				refs, err := pgDependQueryRefs(ctx, view, viewOf(d).query)
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

func capturePGDepend(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
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
				row[i], err = sqltype.Integer(source.Columns[i].Usage.Type, n)
			} else {
				row[i], err = sqltype.OIDValue(source.Columns[i].Usage.Type, uint32(n))
			}
			if err != nil {
				return nil, err
			}
		}
		row[6] = sqltype.String(source.Columns[6].Usage.Type, string(edge.deptype))
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
