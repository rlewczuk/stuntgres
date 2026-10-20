package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func (e *Engine) reownCatalogExpressions(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool) error {
	view := e.catalog.read(next)
	collation := typedef.RemappedCollationCapabilities(next, mapping)
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) || typesOnly {
			return old
		}
		address := old.Address
		address.SubID = 0
		if target, ok := mapping[address]; ok {
			target.SubID = old.Address.SubID
			old.Address = target
		}
		old = e.catalog.Domain.Ref(old.Epoch, old.Address)
		old.Epoch = next.Epoch()
		return old
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}
		if target, ok := mapping[address]; ok {
			address = target
		}
		if d, ok := next.Type(address); ok {
			return d.SQLType
		}
		return old
	}
	// Retagging resolves captured provider descriptors in the destination.
	// Remap source references first, exactly once, before that lookup.
	for address, descriptor := range next.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && (len(domain.DomainChecks) != 0 || domain.DomainDefault != nil) {
			if typesOnly {
				changed := false
				for _, check := range domain.DomainChecks {
					analyze.WalkQueryArgumentExpression(check.Expression, func(node expr.Expr) { changed = changed || node.ResultUsage().Type != typ(node.ResultUsage().Type) })
				}
				if domain.DomainDefault != nil {
					analyze.WalkQueryArgumentExpression(domain.DomainDefault.Expression, func(node expr.Expr) { changed = changed || node.ResultUsage().Type != typ(node.ResultUsage().Type) })
				}
				if !changed {
					continue
				}
			}
			copy := *domain
			copy.DomainChecks = append([]catalog.DomainCheckConstraint(nil), domain.DomainChecks...)
			for i := range copy.DomainChecks {
				check := &copy.DomainChecks[i]
				node, err := remapBoundExpression(check.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				check.Expression = node
				check.Dependencies = append([]catalog.ExpressionDependency(nil), check.Dependencies...)
				for j := range check.Dependencies {
					check.Dependencies[j].Ref = ref(check.Dependencies[j].Ref)
					address := check.Dependencies[j].Ref.Address
					address.SubID = 0
					if provider, exists := view.Descriptor(address); exists {
						check.Dependencies[j].Revision = provider.Revision()
					}
				}
			}
			if domain.DomainDefault != nil {
				definition := *domain.DomainDefault
				node, err := remapBoundExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				definition.Expression = node
				definition.Dependencies = append([]ids.ObjectRef(nil), definition.Dependencies...)
				for i := range definition.Dependencies {
					definition.Dependencies[i] = ref(definition.Dependencies[i])
				}
				copy.DomainDefault = &definition
			}
			next.SetDescriptor(address, &copy)
			continue
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if len(rulesOf(d)) != 0 {
			var err error
			d, err = remapRules(view, d, mapping, ref, typ, collation)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if typesOnly && next.ACLItemImport() == nil && !catalogExpressionTypesChanged(d, typ) {
			continue
		}
		if len(d.ColumnMeta.Defaults) != 0 {
			copy := *d
			copy.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(d.ColumnMeta.Defaults))
			for attribute, definition := range d.ColumnMeta.Defaults {
				node, err := remapBoundExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				definition.Expression = node
				definition.Dependencies = append([]ids.ObjectRef(nil), definition.Dependencies...)
				for i := range definition.Dependencies {
					definition.Dependencies[i] = ref(definition.Dependencies[i])
				}
				copy.ColumnMeta.Defaults[attribute] = definition
			}
			d = &copy
			next.SetDescriptor(address, d)
		}
		if len(d.ColumnMeta.Generations) != 0 {
			copy := *d
			copy.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration, len(d.ColumnMeta.Generations))
			for attribute, definition := range d.ColumnMeta.Generations {
				node, err := remapBoundExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				definition.Expression = node
				planned, err := remapBoundExpression(definition.Planned, ref, nil)
				if err != nil {
					return err
				}
				planned, _, err = retagBoundTypes(view, planned, typ, collation)
				if err != nil {
					return err
				}
				definition.Planned = planned
				definition.Dependencies = append([]ids.ObjectRef(nil), definition.Dependencies...)
				for i := range definition.Dependencies {
					definition.Dependencies[i] = ref(definition.Dependencies[i])
				}
				copy.ColumnMeta.Generations[attribute] = definition
			}
			d = &copy
			next.SetDescriptor(address, d)
		}
		if len(d.Constraints.Check) != 0 {
			copy := *d
			copy.Constraints.Check = append([]catalog.CheckConstraint(nil), d.Constraints.Check...)
			for i := range copy.Constraints.Check {
				c := &copy.Constraints.Check[i]
				node, err := remapBoundExpression(c.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				c.Expression = node
				c.Dependencies = append([]catalog.ExpressionDependency(nil), c.Dependencies...)
				for j := range c.Dependencies {
					c.Dependencies[j].Ref = ref(c.Dependencies[j].Ref)
					if c.Dependencies[j].Ref.Address.ClassID == ids.ProcedureClassID {
						provider, exists := view.Descriptor(c.Dependencies[j].Ref.Address)
						if !exists {
							return fmt.Errorf("stuntgres: missing reconstructed CHECK routine")
						}
						c.Dependencies[j].Revision = provider.Revision()
					}
				}
			}
			if err := refreshGenerationChecks(&copy, view); err != nil {
				return err
			}
			d = &copy
			next.SetDescriptor(address, d)
		}
		if d.SQLIndex != nil {
			copy := *d
			definition := *d.SQLIndex
			definition.Keys = append([]catalog.IndexKey(nil), definition.Keys...)
			for i, key := range definition.Keys {
				if collation != nil {
					capability, err := collation(key.CollationRef)
					if err != nil {
						return err
					}
					definition.Keys[i].CollationRef = capability
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)}]; ok {
						definition.Keys[i].Collation = uint32(target.ObjectID)
					}
				}
				if key.Derived == nil {
					continue
				}
				derived := *key.Derived
				node, err := remapBoundExpression(derived.Definition, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				derived.Definition = node
				node, err = remapBoundExpression(derived.Execution, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				derived.Execution = node
				derived.Dependencies = append([]ids.ObjectRef(nil), derived.Dependencies...)
				for j := range derived.Dependencies {
					derived.Dependencies[j] = ref(derived.Dependencies[j])
				}
				definition.Keys[i].Derived = &derived
			}
			if predicate := definition.Predicate; predicate != nil {
				p := *predicate
				node, err := remapBoundExpression(p.Definition, ref, nil)
				if err != nil {
					return err
				}
				p.Definition, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(p.Execution, ref, nil)
				if err != nil {
					return err
				}
				p.Execution, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				p.Dependencies = append([]ids.ObjectRef(nil), p.Dependencies...)
				for j := range p.Dependencies {
					p.Dependencies[j] = ref(p.Dependencies[j])
				}
				definition.Predicate = &p
			}
			copy.SQLIndex = &definition
			d = &copy
			next.SetDescriptor(address, d)
		}
		if hierarchyRelation(d) {
			var err error
			d, err = remapPartitionDescriptor(view, d, ref, typ, mapping, typesOnly, collation)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if len(d.Security.Policies) != 0 {
			var err error
			d, err = remapPolicies(view, d, mapping, ref, typ, collation)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if d.View == nil {
			continue
		}
		definition, err := remapFrozenPresentation(view, viewOf(d), mapping, ref, typ, collation)
		if err != nil {
			return err
		}
		copy := *d
		copy.View = viewPayloadOf(definition)
		next.SetDescriptor(address, &copy)
	}
	return nil
}
