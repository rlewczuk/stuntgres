package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (e *Engine) reownCatalogExpressions(next *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool) error {
	view := e.catalog.read(next)
	collation := remappedCollationCapabilities(next, mapping)
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
		if domain, ok := descriptor.(*typeDescriptor); ok && len(domain.DomainChecks) != 0 {
			if typesOnly {
				changed := false
				for _, check := range domain.DomainChecks {
					walkQueryArgumentExpression(check.Expression, func(node expr.Expr) { changed = changed || node.ResultUsage().Type != typ(node.ResultUsage().Type) })
				}
				if !changed {
					continue
				}
			}
			copy := *domain
			copy.DomainChecks = append([]domainCheckConstraint(nil), domain.DomainChecks...)
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
				check.Dependencies = append([]expressionDependency(nil), check.Dependencies...)
				for j := range check.Dependencies {
					check.Dependencies[j].Ref = ref(check.Dependencies[j].Ref)
					address := check.Dependencies[j].Ref.Address
					address.SubID = 0
					if provider, exists := view.Descriptor(address); exists {
						check.Dependencies[j].Revision = provider.Revision()
					}
				}
			}
			next.SetDescriptor(address, &copy)
			continue
		}
		d, ok := descriptor.(*relationDescriptor)
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
		if len(d.Defaults) != 0 {
			copy := *d
			copy.Defaults = make(map[int32]columnDefault, len(d.Defaults))
			for attribute, definition := range d.Defaults {
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
				copy.Defaults[attribute] = definition
			}
			d = &copy
			next.SetDescriptor(address, d)
		}
		if len(d.Generations) != 0 {
			copy := *d
			copy.Generations = make(map[int32]columnGeneration, len(d.Generations))
			for attribute, definition := range d.Generations {
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
				copy.Generations[attribute] = definition
			}
			d = &copy
			next.SetDescriptor(address, d)
		}
		if len(d.CheckConstraints) != 0 {
			copy := *d
			copy.CheckConstraints = append([]checkConstraint(nil), d.CheckConstraints...)
			for i := range copy.CheckConstraints {
				c := &copy.CheckConstraints[i]
				node, err := remapBoundExpression(c.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				c.Expression = node
				c.Dependencies = append([]expressionDependency(nil), c.Dependencies...)
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
			definition.Keys = append([]indexKey(nil), definition.Keys...)
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
		if len(d.Policies) != 0 {
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
