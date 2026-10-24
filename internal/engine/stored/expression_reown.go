package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// ReownExpressions remaps and retags every stored expression of next after
// its objects were renumbered by mapping (or, with typesOnly, after only
// their types changed). view is the catalog view of next; its owner supplies
// the reference domain.
func ReownExpressions(view catalog.Read, next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, routineRemap *catalog.RoutineRemap) error {
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		return reownExpressionReference(view, next, mapping, typesOnly, old)
	}
	createdRemap := routineRemap == nil
	if routineRemap == nil {
		leaf := func(old *sqltype.Descriptor) *sqltype.Descriptor {
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
		routineRemap = NewRoutineRemap(view, mapping, leaf, nil, typesOnly)
	}
	if err := ReownInstalledDomainChecks(view, next, mapping, typesOnly, routineRemap); err != nil {
		return err
	}
	if createdRemap && !typesOnly {
		if err := routine.RebindSQLDefinitions(next, routineRemap); err != nil {
			return err
		}
	}
	typ, collation, procedure := routineRemap.Type, routineRemap.Collation, routineRemap.Procedure
	mapper := &ViewQueryRemapper{Catalog: view, Ref: func(ref ids.ObjectRef) ids.ObjectRef { return ref }, Type: typ, Collation: collation, Procedure: procedure}
	retag := func(node expr.Expr) (expr.Expr, bool, error) {
		return mapExpressionWithProcedure(view, nil, node, typ, mapper.Nested, nil, false, nil, procedure, collation)
	}
	if typesOnly {
		if err := reownSQLRoutineTypes(view, next, typ, routineRemap); err != nil {
			return err
		}
	}
	// Retagging resolves captured provider descriptors in the destination.
	// Remap source references first, exactly once, before that lookup.
	for address, descriptor := range next.Descriptors() {
		if domain, ok := descriptor.(*catalog.Type); ok && (len(domain.DomainChecks) != 0 || domain.DomainDefault != nil) {
			if installedDomainCheckRecipes(domain) && domain.DomainDefault == nil {
				continue
			}
			if typesOnly {
				changed := false
				for _, check := range domain.DomainChecks {
					changed = changed || routineExpressionChanged(view, check.Expression, typ)
				}
				if domain.DomainDefault != nil {
					changed = changed || routineExpressionChanged(view, domain.DomainDefault.Expression, typ)
				}
				if !changed {
					continue
				}
			}
			copy := *domain
			if !installedDomainCheckRecipes(domain) {
				var err error
				copy.DomainChecks, err = reownDomainCheckDefinitions(view, domain.DomainChecks, ref, retag)
				if err != nil {
					return err
				}
			}
			if domain.DomainDefault != nil {
				definition := *domain.DomainDefault
				node, err := RemapExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
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
		if d.HasTriggers() {
			var err error
			d, err = RemapTriggers(view, d, mapping, ref, typ, collation, procedure)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if len(RulesOf(d)) != 0 {
			var err error
			d, err = remapRules(view, d, mapping, ref, typ, collation, procedure)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if typesOnly && next.ACLItemImport() == nil && !ExpressionDescriptorsChanged(view, d, typ) {
			continue
		}
		if len(d.ColumnMeta.Defaults) != 0 {
			copy := *d
			copy.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(d.ColumnMeta.Defaults))
			for attribute, definition := range d.ColumnMeta.Defaults {
				node, err := RemapExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
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
				node, err := RemapExpression(definition.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
				if err != nil {
					return err
				}
				definition.Expression = node
				planned, err := RemapExpression(definition.Planned, ref, nil)
				if err != nil {
					return err
				}
				planned, _, err = retag(planned)
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
				node, err := RemapExpression(c.Expression, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
				if err != nil {
					return err
				}
				c.Expression = node
				c.Dependencies = append([]catalog.ExpressionDependency(nil), c.Dependencies...)
				for j := range c.Dependencies {
					c.Dependencies[j].Ref = ref(c.Dependencies[j].Ref)
				}
			}
			if err := RefreshGenerationChecks(&copy, view); err != nil {
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
				node, err := RemapExpression(derived.Definition, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
				if err != nil {
					return err
				}
				derived.Definition = node
				node, err = RemapExpression(derived.Execution, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retag(node)
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
				node, err := RemapExpression(p.Definition, ref, nil)
				if err != nil {
					return err
				}
				p.Definition, _, err = retag(node)
				if err != nil {
					return err
				}
				node, err = RemapExpression(p.Execution, ref, nil)
				if err != nil {
					return err
				}
				p.Execution, _, err = retag(node)
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
		if catalog.HierarchyRelation(d) {
			var err error
			d, err = RemapPartitionDescriptor(view, d, ref, typ, mapping, typesOnly, collation, procedure)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if len(d.Security.Policies) != 0 {
			var err error
			d, err = remapPolicies(view, d, mapping, ref, typ, collation, procedure)
			if err != nil {
				return err
			}
			next.SetDescriptor(address, d)
		}
		if d.View == nil {
			continue
		}
		definition, err := remapView(view, ViewOf(d), mapping, ref, typ, collation, nil, procedure)
		if err != nil {
			return err
		}
		copy := *d
		copy.View = ViewPayload(definition)
		next.SetDescriptor(address, &copy)
	}
	if routineRemap != nil {
		return routineRemap.Finish()
	}
	return nil
}
