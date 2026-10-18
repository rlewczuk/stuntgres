package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (e *Engine) reownCatalogExpressions(next *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool) error {
	view := CatalogRead{owner: e, state: next}
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
		old = e.catalog.domain.Ref(old.Epoch, old.Address)
		old.Epoch = next.epoch
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
		if d, ok := next.descriptors[address].(*typeDescriptor); ok {
			return d.typ
		}
		return old
	}
	// Retagging resolves captured provider descriptors in the destination.
	// Remap source references first, exactly once, before that lookup.
	for address, descriptor := range next.descriptors {
		if domain, ok := descriptor.(*typeDescriptor); ok && len(domain.domainChecks) != 0 {
			if typesOnly {
				changed := false
				for _, check := range domain.domainChecks {
					walkQueryArgumentExpression(check.expression, func(node BoundExpr) { changed = changed || node.ResultUsage().Type != typ(node.ResultUsage().Type) })
				}
				if !changed {
					continue
				}
			}
			copy := *domain
			copy.domainChecks = append([]domainCheckConstraint(nil), domain.domainChecks...)
			for i := range copy.domainChecks {
				check := &copy.domainChecks[i]
				node, err := remapBoundExpression(check.expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				check.expression = node
				check.dependencies = append([]expressionDependency(nil), check.dependencies...)
				for j := range check.dependencies {
					check.dependencies[j].ref = ref(check.dependencies[j].ref)
					address := check.dependencies[j].ref.Address
					address.SubID = 0
					if provider, exists := view.Descriptor(address); exists {
						check.dependencies[j].revision = provider.Revision()
					}
				}
			}
			next.descriptors[address] = &copy
			continue
		}
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if len(d.rules) != 0 {
			var err error
			d, err = remapRules(view, d, mapping, ref, typ, collation)
			if err != nil {
				return err
			}
			next.descriptors[address] = d
		}
		if typesOnly && next.aclItemImport == nil && !catalogExpressionTypesChanged(d, typ) {
			continue
		}
		if len(d.defaults) != 0 {
			copy := *d
			copy.defaults = make(map[int32]columnDefault, len(d.defaults))
			for attribute, definition := range d.defaults {
				node, err := remapBoundExpression(definition.expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				definition.expression = node
				definition.dependencies = append([]ids.ObjectRef(nil), definition.dependencies...)
				for i := range definition.dependencies {
					definition.dependencies[i] = ref(definition.dependencies[i])
				}
				copy.defaults[attribute] = definition
			}
			d = &copy
			next.descriptors[address] = d
		}
		if len(d.generations) != 0 {
			copy := *d
			copy.generations = make(map[int32]columnGeneration, len(d.generations))
			for attribute, definition := range d.generations {
				node, err := remapBoundExpression(definition.expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				definition.expression = node
				planned, err := remapBoundExpression(definition.planned, ref, nil)
				if err != nil {
					return err
				}
				planned, _, err = retagBoundTypes(view, planned, typ, collation)
				if err != nil {
					return err
				}
				definition.planned = planned
				definition.dependencies = append([]ids.ObjectRef(nil), definition.dependencies...)
				for i := range definition.dependencies {
					definition.dependencies[i] = ref(definition.dependencies[i])
				}
				copy.generations[attribute] = definition
			}
			d = &copy
			next.descriptors[address] = d
		}
		if len(d.checkConstraints) != 0 {
			copy := *d
			copy.checkConstraints = append([]checkConstraint(nil), d.checkConstraints...)
			for i := range copy.checkConstraints {
				c := &copy.checkConstraints[i]
				node, err := remapBoundExpression(c.expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				c.expression = node
				c.dependencies = append([]expressionDependency(nil), c.dependencies...)
				for j := range c.dependencies {
					c.dependencies[j].ref = ref(c.dependencies[j].ref)
					if c.dependencies[j].ref.Address.ClassID == ids.ProcedureClassID {
						provider, exists := view.Descriptor(c.dependencies[j].ref.Address)
						if !exists {
							return fmt.Errorf("stuntgres: missing reconstructed CHECK routine")
						}
						c.dependencies[j].revision = provider.Revision()
					}
				}
			}
			if err := refreshGenerationChecks(&copy, view); err != nil {
				return err
			}
			d = &copy
			next.descriptors[address] = d
		}
		if d.sqlIndex != nil {
			copy := *d
			definition := *d.sqlIndex
			definition.keys = append([]indexKey(nil), definition.keys...)
			for i, key := range definition.keys {
				if collation != nil {
					capability, err := collation(key.collationRef)
					if err != nil {
						return err
					}
					definition.keys[i].collationRef = capability
					if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.collation)}]; ok {
						definition.keys[i].collation = uint32(target.ObjectID)
					}
				}
				if key.derived == nil {
					continue
				}
				derived := *key.derived
				node, err := remapBoundExpression(derived.definition, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				derived.definition = node
				node, err = remapBoundExpression(derived.execution, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				derived.execution = node
				derived.dependencies = append([]ids.ObjectRef(nil), derived.dependencies...)
				for j := range derived.dependencies {
					derived.dependencies[j] = ref(derived.dependencies[j])
				}
				definition.keys[i].derived = &derived
			}
			if predicate := definition.predicate; predicate != nil {
				p := *predicate
				node, err := remapBoundExpression(p.definition, ref, nil)
				if err != nil {
					return err
				}
				p.definition, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(p.execution, ref, nil)
				if err != nil {
					return err
				}
				p.execution, _, err = retagBoundTypes(view, node, typ, collation)
				if err != nil {
					return err
				}
				p.dependencies = append([]ids.ObjectRef(nil), p.dependencies...)
				for j := range p.dependencies {
					p.dependencies[j] = ref(p.dependencies[j])
				}
				definition.predicate = &p
			}
			copy.sqlIndex = &definition
			d = &copy
			next.descriptors[address] = d
		}
		if hierarchyRelation(d) {
			var err error
			d, err = remapPartitionDescriptor(view, d, ref, typ, mapping, typesOnly, collation)
			if err != nil {
				return err
			}
			next.descriptors[address] = d
		}
		if len(d.policies) != 0 {
			var err error
			d, err = remapPolicies(view, d, mapping, ref, typ, collation)
			if err != nil {
				return err
			}
			next.descriptors[address] = d
		}
		if d.view == nil {
			continue
		}
		definition, err := remapFrozenPresentation(view, d.view, mapping, ref, typ, collation)
		if err != nil {
			return err
		}
		copy := *d
		copy.view = definition
		next.descriptors[address] = &copy
	}
	return nil
}
