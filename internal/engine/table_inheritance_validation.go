package engine

import (
	"fmt"
	"slices"
)

// Retained TEMP definitions keep their original row-reference epoch across a
// persistent replacement. Compare at current row authority without changing
// either descriptor or remapping external provider references.
func sameInheritedExpression(mapped, child BoundExpr, d *relationDescriptor, v CatalogRead) bool {
	if sameGroupExpression(mapped, child) {
		return true
	}
	identity := createLikeInput{source: d}
	current, err := identity.remap(child, d, v)
	return err == nil && sameGroupExpression(mapped, current)
}

func validateInheritance(v CatalogRead, d *relationDescriptor) error {
	graph := d.inheritance
	if graph == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid table inheritance graph") }
	if d.relationKind != tableRelation || len(graph.parents) == 0 || len(graph.columns) != len(d.columns) {
		return invalid()
	}
	childNS, ok := v.state.descriptors[d.namespace].(*namespaceDescriptor)
	if !ok {
		return invalid()
	}
	seen := make(map[ObjectAddress]bool, len(graph.parents))
	for _, address := range graph.parents {
		parent, ok := v.state.descriptors[address].(*relationDescriptor)
		if !ok || parent.address != address || parent.relationKind != tableRelation || address.SubID != 0 || seen[address] || address == d.address {
			return invalid()
		}
		seen[address] = true
		parentNS, ok := v.state.descriptors[parent.namespace].(*namespaceDescriptor)
		if !ok || parentNS.temporary != (SessionToken{}) && parentNS.temporary != childNS.temporary {
			return invalid()
		}
	}
	for slot, attribute := range d.attributes {
		provenance, ok := graph.columns[attribute]
		if !ok || !provenance.local && len(provenance.origins) == 0 || (provenance.explicitDefault || provenance.explicitGeneration) && !provenance.local {
			return invalid()
		}
		origins := make(map[ObjectAddress]bool, len(provenance.origins))
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.constraint != 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.state.descriptors[origin.parent].(*relationDescriptor)
			sourceSlot := slices.Index(parent.attributes, origin.attribute)
			if sourceSlot < 0 || parent.columns[sourceSlot].Name != d.columns[slot].Name || inheritanceColumnConflict(v, parent.columns[sourceSlot], d.columns[slot], false) != nil {
				return invalid()
			}
		}
		for _, parentAddress := range graph.parents {
			parent := v.state.descriptors[parentAddress].(*relationDescriptor)
			if _, ok := parent.index[d.columns[slot].Name]; ok && !origins[parentAddress] {
				return invalid()
			}
		}
	}
	for _, parentAddress := range graph.parents {
		parent := v.state.descriptors[parentAddress].(*relationDescriptor)
		mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.columns))}
		for i, column := range parent.columns {
			target, exists := d.index[column.Name]
			if !exists || target < 0 || target >= len(d.attributes) {
				return invalid()
			}
			mapper.targetSlots[i] = target
		}
		for _, constraint := range parent.notNullConstraints {
			sourceSlot := slices.Index(parent.attributes, constraint.Attribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.attributes[mapper.targetSlots[sourceSlot]]
			if !slices.ContainsFunc(graph.notNull[target].origins, func(origin inheritanceOrigin) bool {
				return origin.parent == parentAddress && origin.constraint == constraint.OID && origin.attribute == constraint.Attribute
			}) {
				return invalid()
			}
		}
		for _, constraint := range parent.checkConstraints {
			if !constraint.NoInherit && !slices.ContainsFunc(graph.checks[constraint.Name].origins, func(origin inheritanceOrigin) bool {
				return origin.parent == parentAddress && origin.constraint == constraint.OID
			}) {
				return invalid()
			}
		}
		for sourceAttribute, generation := range parent.generations {
			sourceSlot := slices.Index(parent.attributes, sourceAttribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.attributes[mapper.targetSlots[sourceSlot]]
			child, ok := d.generations[target]
			if !ok {
				return invalid()
			}
			if !graph.columns[target].explicitGeneration {
				remapped, err := mapper.remap(generation.expression, d, v)
				if err != nil || !sameInheritedExpression(remapped, child.expression, d, v) {
					return invalid()
				}
			}
		}
	}
	for attribute, provenance := range graph.notNull {
		child := slices.IndexFunc(d.notNullConstraints, func(c notNullConstraint) bool { return c.Attribute == attribute })
		if child < 0 || !provenance.local && len(provenance.origins) == 0 {
			return invalid()
		}
		origins := make(map[ObjectAddress]bool)
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.constraint == 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.state.descriptors[origin.parent].(*relationDescriptor)
			found := slices.ContainsFunc(parent.notNullConstraints, func(c notNullConstraint) bool {
				return c.OID == origin.constraint && c.Attribute == origin.attribute && c.OID != d.notNullConstraints[child].OID
			})
			if !found {
				return invalid()
			}
			sourceSlot := slices.Index(parent.attributes, origin.attribute)
			targetSlot := slices.Index(d.attributes, attribute)
			if sourceSlot < 0 || targetSlot < 0 || parent.columns[sourceSlot].Name != d.columns[targetSlot].Name {
				return invalid()
			}
		}
	}
	for name, provenance := range graph.checks {
		child := slices.IndexFunc(d.checkConstraints, func(c checkConstraint) bool { return c.Name == name })
		if child < 0 || !provenance.local && len(provenance.origins) == 0 || d.checkConstraints[child].NoInherit && len(provenance.origins) != 0 {
			return invalid()
		}
		origins := make(map[ObjectAddress]bool)
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.attribute != 0 || origin.constraint == 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.state.descriptors[origin.parent].(*relationDescriptor)
			source := slices.IndexFunc(parent.checkConstraints, func(c checkConstraint) bool {
				return c.OID == origin.constraint && c.Name == name && !c.NoInherit && c.OID != d.checkConstraints[child].OID
			})
			if source < 0 {
				return invalid()
			}
			mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.columns))}
			for i, c := range parent.columns {
				mapper.targetSlots[i] = d.index[c.Name]
			}
			node, err := mapper.remap(parent.checkConstraints[source].expression, d, v)
			if err != nil || !sameInheritedExpression(node, d.checkConstraints[child].expression, d, v) {
				return invalid()
			}
		}
	}
	for _, constraint := range d.notNullConstraints {
		if _, ok := graph.notNull[constraint.Attribute]; !ok {
			return invalid()
		}
	}
	for _, constraint := range d.checkConstraints {
		if _, ok := graph.checks[constraint.Name]; !ok {
			return invalid()
		}
	}
	// An explicit DFS validates imported/internal candidates as well as CREATE.
	visiting := make(map[ObjectAddress]bool)
	visited := make(map[ObjectAddress]bool)
	var visit func(*relationDescriptor) bool
	visit = func(current *relationDescriptor) bool {
		if visiting[current.address] {
			return false
		}
		if visited[current.address] {
			return true
		}
		visiting[current.address] = true
		if current.inheritance != nil {
			for _, address := range current.inheritance.parents {
				parent, ok := v.state.descriptors[address].(*relationDescriptor)
				if !ok || !visit(parent) {
					return false
				}
			}
		}
		delete(visiting, current.address)
		visited[current.address] = true
		return true
	}
	if !visit(d) {
		return invalid()
	}
	return nil
}
