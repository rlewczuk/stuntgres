package pgcatalog

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// DependEdge is one pg_depend row before column encoding.
type DependEdge struct {
	Dependent, Referenced ids.ObjectAddress
	DepType               byte
}

// CollectDepend gathers the dependency edges of a captured catalog.
func CollectDepend(ctx context.Context, view catalog.Read, runtime Runtime) ([]DependEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var edges []DependEdge
	add := func(dependent, referenced ids.ObjectAddress, flavor byte) {
		if referenced.ObjectID != 0 && !runtime.Pinned(view, referenced) {
			edges = append(edges, DependEdge{dependent, referenced, flavor})
		}
	}
	attribute := func(relation ids.ObjectAddress, sub int32) ids.ObjectAddress { relation.SubID = sub; return relation }
	expression := func(owner ids.ObjectAddress, expr expr.Expr, parent ids.ObjectAddress, localFlavor byte) error {
		refs, err := runtime.ExpressionReferences(ctx, view, expr)
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
		case *catalog.AccessMethod:
			if !catalog.AccessMethodSeeded(d) {
				for _, dependency := range catalog.AccessMethodDependencies(d) {
					add(d.Address(), dependency.Address, dependency.Kind)
				}
			}
		case *catalog.Cast:
			for _, dependency := range catalog.CastDependencies(d) {
				add(d.Address(), dependency, 'n')
			}
		case *catalog.OperatorClass:
			if !catalog.OperatorClassSeeded(d) {
				for _, dependency := range catalog.OperatorFamilyDependencies(view, d) {
					add(d.Address(), dependency.Address, dependency.Kind)
				}
			}
		case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
			if !catalog.OperatorFamilySeeded(d) {
				for _, dependency := range catalog.OperatorFamilyDependencies(view, d) {
					add(d.Address(), dependency.Address, dependency.Kind)
				}
			}
		case *catalog.Operator:
			add(d.Address(), d.Extension, 'e')
			if !runtime.OperatorSeeded(d) {
				for _, address := range runtime.OperatorDependencies(view, d) {
					add(d.Address(), address, 'n')
				}
			}
		case *catalog.ForeignObject:
			for _, address := range ForeignDependencies(d) {
				if address != (ids.ObjectAddress{}) {
					add(d.Address(), address, 'n')
				}
			}
		case *catalog.Extension:
			add(d.Address(), d.TargetNamespace, 'n')
			prerequisites, err := catalog.ExtensionPrerequisites(view, d)
			if err != nil {
				return nil, err
			}
			for _, required := range prerequisites {
				add(d.Address(), required, 'n')
			}
		case *catalog.Procedure:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			for address := range catalog.OwnedAggregateDependencies(d) {
				add(d.Address(), address, 'n')
			}
			if d.SQL != nil {
				add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}, 'n')
				for _, formal := range d.SQL.Formals {
					add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(formal.Usage.Type.ID())}, 'n')
				}
				if d.SQLBody != nil {
					for _, dependency := range d.SQLBody.Dependencies() {
						add(d.Address(), dependency.Ref.Address, 'n')
					}
				}
				if d.SQLDefaults != nil {
					for _, dependency := range d.SQLDefaults.Dependencies() {
						add(d.Address(), dependency.Ref.Address, 'n')
					}
				}
			}
			if runtime.TypeProviderProcedure(d) {
				if d.Name() == "bqarr_in" || d.Name() == "cube_in" {
					add(d.Address(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}, 'n')
				} else {
					ext := view.MustExtension(d.Extension)
					add(d.Address(), ext.Members[0], 'n')
				}
			}
		case *catalog.TSTemplate:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			add(d.Address(), d.Init, 'n')
			add(d.Address(), d.Lexize, 'n')
		case *catalog.TSDictionary:
			add(d.Address(), d.Extension, 'e')
			add(d.Address(), d.Namespace(), 'n')
			add(d.Address(), d.Template, 'n')
		case *catalog.Collation:
			if !d.Seeded() {
				add(d.Address(), d.Namespace(), 'n')
			}
		case *catalog.Conversion:
			if !d.Seeded() {
				add(d.Address(), d.Namespace(), 'n')
				add(d.Address(), d.Handler, 'n')
			}
		case *catalog.Type:
			if runtime.Pinned(view, d.Address()) {
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
					if d.DomainDefault != nil {
						for _, ref := range d.DomainDefault.Dependencies {
							add(d.Address(), ref.Address, 'n')
						}
					}
				}
			}
			if d.DomainNotNull != nil {
				add(ids.ObjectAddress{ClassID: 2606, ObjectID: d.DomainNotNull.OID}, d.Address(), 'a')
			}
			for _, check := range d.DomainChecks {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}
				for _, dependency := range check.Dependencies {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					add(owner, dependency.Ref.Address, 'n')
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
			for _, policy := range d.Security.Policies {
				owner := ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}
				add(owner, d.Address(), 'a')
				if err := policy.VisitExpressions(func(condition expr.Expr) error {
					return expression(owner, condition, d.Address(), 'n')
				}); err != nil {
					return nil, err
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
					classes := make(map[uint32]bool)
					for _, key := range definition.Keys {
						if oid := key.Class.OID(); oid != 0 && !classes[oid] {
							classes[oid] = true
							add(d.Address(), ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(oid)}, 'n')
						}
					}
					continue
				}
				// Simple/INCLUDE ownership is a separate pass from expressions.
				attrs := make(map[int32]struct{})
				for _, key := range definition.Keys {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if sub, simple := runtime.IndexSimpleAttribute(definition.Parent, key); simple {
						// Parenthesized direct Vars retain simple-key ownership.
						attrs[sub] = struct{}{}
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
				addresses, err := runtime.IndexReferences(ctx, view, definition)
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
					add(d.Address(), parent.Relation, 'n')
				}
			}
			if d.Membership != nil {
				add(d.Address(), d.Membership.Parent, 'a')
			}
			if d.Partition != nil {
				addresses, err := runtime.PartitionReferences(ctx, view, d)
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
			for _, c := range d.Constraints.NotNull {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				add(ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, attribute(d.Address(), c.Attribute), 'a')
			}
			for _, c := range d.Constraints.Unique {
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
			for _, f := range d.Constraints.ForeignKeys {
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: f.OID}
				for _, sub := range f.Attributes {
					add(owner, attribute(d.Address(), sub), 'a')
				}
				for _, sub := range f.Referenced {
					add(owner, attribute(f.Target, sub), 'n')
				}
				add(owner, f.Index, 'n')
			}
			for _, c := range d.Constraints.Exclusion {
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
					refs, err := runtime.ExpressionReferences(ctx, view, key.Derived.Definition)
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
					refs, err := runtime.ExpressionReferences(ctx, view, predicate.Definition)
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
			for _, c := range d.Constraints.Check {
				owner := ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}
				refs, err := runtime.ExpressionReferences(ctx, view, c.Expression)
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
			for sub, definition := range d.ColumnMeta.Defaults {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.Address(), sub), 'a')
				if err := expression(owner, definition.Expression, d.Address(), 'n'); err != nil {
					return nil, err
				}
			}
			for sub, definition := range d.ColumnMeta.Generations {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				owner := ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}
				add(owner, attribute(d.Address(), sub), 'i')
				if err := expression(owner, definition.Expression, d.Address(), 'n'); err != nil {
					return nil, err
				}
			}
			if err := d.VisitRules(func(rule catalog.RuleFact) error {
				owner := ids.ObjectAddress{ClassID: 2618, ObjectID: rule.OID}
				add(owner, d.Address(), 'a')
				for _, ref := range rule.References {
					add(owner, ref, 'n')
				}
				return nil
			}); err != nil {
				return nil, err
			}
			if d.View != nil {
				owner := ids.ObjectAddress{ClassID: 2618, ObjectID: d.View.RuleOID()}
				add(owner, d.Address(), 'i')
				refs, err := runtime.ViewReferences(ctx, view, d)
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

func capturePGDepend(in Input) ([][]sqltype.Value, error) {
	ctx, catalog, source := in.Context, in.Catalog, in.Source
	edges, err := CollectDepend(ctx, catalog, in.Runtime)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, len(edges))
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 7)
		for i, n := range [6]int64{int64(edge.Dependent.ClassID), int64(edge.Dependent.ObjectID), int64(edge.Dependent.SubID), int64(edge.Referenced.ClassID), int64(edge.Referenced.ObjectID), int64(edge.Referenced.SubID)} {
			if i == 2 || i == 5 {
				row[i], err = sqltype.Integer(source.Columns[i].Usage.Type, n)
			} else {
				row[i], err = sqltype.OIDValue(source.Columns[i].Usage.Type, uint32(n))
			}
			if err != nil {
				return nil, err
			}
		}
		row[6] = sqltype.String(source.Columns[6].Usage.Type, string(edge.DepType))
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func ForeignDependencies(d *catalog.ForeignObject) [4]ids.ObjectAddress {
	return [4]ids.ObjectAddress{d.Wrapper, d.Server, d.Handler, d.Validator}
}
