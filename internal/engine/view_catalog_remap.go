package engine

import "stuntgres/internal/sqltype"

func (e *Engine) reownCatalogExpressions(next *catalogState, mapping map[ObjectAddress]ObjectAddress, typesOnly bool) error {
	view := CatalogRead{owner: e, state: next}
	collation := remappedCollationCapabilities(next, mapping)
	ref := func(old ObjectRef) ObjectRef {
		if old == (ObjectRef{}) || typesOnly {
			return old
		}
		address := old.Address
		address.SubID = 0
		if target, ok := mapping[address]; ok {
			target.SubID = old.Address.SubID
			old.Address = target
		}
		old.owner = e.owner
		old.Epoch = next.epoch
		return old
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.ID())}
		if target, ok := mapping[address]; ok {
			address = target
		}
		if d, ok := next.descriptors[address].(*typeDescriptor); ok {
			return d.typ
		}
		return old
	}
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if typesOnly && !catalogExpressionTypesChanged(d, typ) {
			continue
		}
		if len(d.defaults) != 0 {
			copy := *d
			copy.defaults = make(map[int32]columnDefault, len(d.defaults))
			for attribute, definition := range d.defaults {
				node, _, err := retagBoundTypes(view, definition.expression, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.expression = node
				definition.dependencies = append([]ObjectRef(nil), definition.dependencies...)
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
				node, _, err := retagBoundTypes(view, definition.expression, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.expression = node
				planned, _, err := retagBoundTypes(view, definition.planned, typ, collation)
				if err != nil {
					return err
				}
				planned, err = remapBoundExpression(planned, ref, nil)
				if err != nil {
					return err
				}
				definition.planned = planned
				definition.dependencies = append([]ObjectRef(nil), definition.dependencies...)
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
				node, _, err := retagBoundTypes(view, c.expression, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				c.expression = node
				c.dependencies = append([]expressionDependency(nil), c.dependencies...)
				for j := range c.dependencies {
					c.dependencies[j].ref = ref(c.dependencies[j].ref)
				}
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
					if target, ok := mapping[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.collation)}]; ok {
						definition.keys[i].collation = uint32(target.ObjectID)
					}
				}
				if key.derived == nil {
					continue
				}
				derived := *key.derived
				node, _, err := retagBoundTypes(view, derived.definition, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				derived.definition = node
				node, _, err = retagBoundTypes(view, derived.execution, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				derived.execution = node
				derived.dependencies = append([]ObjectRef(nil), derived.dependencies...)
				for j := range derived.dependencies {
					derived.dependencies[j] = ref(derived.dependencies[j])
				}
				definition.keys[i].derived = &derived
			}
			if predicate := definition.predicate; predicate != nil {
				p := *predicate
				node, _, err := retagBoundTypes(view, p.definition, typ, collation)
				if err != nil {
					return err
				}
				p.definition, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				node, _, err = retagBoundTypes(view, p.execution, typ, collation)
				if err != nil {
					return err
				}
				p.execution, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				p.dependencies = append([]ObjectRef(nil), p.dependencies...)
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
		if d.view == nil {
			continue
		}
		definition := *d.view
		if collation != nil && len(d.view.collations) != 0 {
			definition.collations = make(map[viewLiteralPath]uint32, len(d.view.collations))
			for path, id := range d.view.collations {
				if target, ok := mapping[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(id)}]; ok {
					id = uint32(target.ObjectID)
				}
				definition.collations[path] = id
			}
		}
		// Namespace metadata has only labels, occurrence/frame slots and stable
		// attribute SubIDs. It is immutable and has no catalog-generation refs
		// or type pointers; the remapped leaves/tree supply its new identities.
		definition.sources = append([]viewLeaf(nil), definition.sources...)
		for i := range definition.sources {
			definition.sources[i].ref = ref(definition.sources[i].ref)
		}
		if len(d.view.literals) != 0 {
			definition.literals = make(map[viewLiteralPath]literalExpression, len(d.view.literals))
			for path, literal := range d.view.literals {
				node, _, err := retagBoundTypes(view, literal, typ, collation)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.literals[path] = node.(literalExpression)
			}
		}
		if len(d.view.castUsages) != 0 {
			definition.castUsages = make(map[viewLiteralPath]sqltype.Usage, len(d.view.castUsages))
			for path, usage := range d.view.castUsages {
				usage.Type = typ(usage.Type)
				if collation != nil {
					var err error
					usage.Collation, err = collation(usage.Collation)
					if err != nil {
						return err
					}
				}
				definition.castUsages[path] = usage
			}
		}
		definition.sequenceRefs = make(map[int]ObjectRef, len(d.view.sequenceRefs))
		for offset, old := range d.view.sequenceRefs {
			definition.sequenceRefs[offset] = ref(old)
		}
		definition.dependencies = append([]expressionDependency(nil), definition.dependencies...)
		for i := range definition.dependencies {
			definition.dependencies[i].ref = ref(definition.dependencies[i].ref)
		}
		root, err := remapViewQuery(view, definition.query.Root, ref, typ, collation)
		if err != nil {
			return err
		}
		query := *definition.query
		query.Root = root
		definition.query = &query
		definition.frames, definition.queries = collectQueryFrames(root)
		copy := *d
		copy.view = &definition
		next.descriptors[address] = &copy
	}
	return nil
}
