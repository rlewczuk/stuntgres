package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Retained TEMP definitions keep their original row-reference epoch across a
// persistent replacement. Compare at current row authority without changing
// either descriptor or remapping external provider references.
func sameInheritedExpression(mapped, child expr.Expr, d *relationDescriptor, v CatalogRead) bool {
	if sameGroupExpression(mapped, child) {
		return true
	}
	identity := createLikeInput{source: d}
	current, err := identity.remap(child, d, v)
	return err == nil && sameGroupExpression(mapped, current)
}

func validateInheritance(v CatalogRead, d *relationDescriptor) error {
	graph := d.Inheritance
	if graph == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid table inheritance graph") }
	if d.RelationKind != tableRelation || len(graph.parents) == 0 || len(graph.columns) != len(d.Columns) {
		return invalid()
	}
	childNS, ok := v.Namespace(d.namespace)
	if !ok {
		return invalid()
	}
	seen := make(map[ids.ObjectAddress]bool, len(graph.parents))
	for _, address := range graph.parents {
		parent, ok := v.Relation(address)
		if !ok || parent.address != address || parent.RelationKind != tableRelation || address.SubID != 0 || seen[address] || address == d.address {
			return invalid()
		}
		seen[address] = true
		parentNS, ok := v.Namespace(parent.namespace)
		if !ok || parentNS.Temporary != (ids.SessionToken{}) && parentNS.Temporary != childNS.Temporary {
			return invalid()
		}
	}
	for slot, attribute := range d.Attributes {
		provenance, ok := graph.columns[attribute]
		if !ok || !provenance.local && len(provenance.origins) == 0 {
			return invalid()
		}
		origins := make(map[ids.ObjectAddress]bool, len(provenance.origins))
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.constraint != 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.MustRelation(origin.parent)
			sourceSlot := slices.Index(parent.Attributes, origin.attribute)
			if sourceSlot < 0 || parent.Columns[sourceSlot].Name != d.Columns[slot].Name || inheritanceColumnConflict(v, parent.Columns[sourceSlot], d.Columns[slot], false) != nil {
				return invalid()
			}
		}
		for _, parentAddress := range graph.parents {
			parent := v.MustRelation(parentAddress)
			if _, ok := parent.Index[d.Columns[slot].Name]; ok && !origins[parentAddress] {
				return invalid()
			}
		}
	}
	for _, parentAddress := range graph.parents {
		parent := v.MustRelation(parentAddress)
		mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.Columns))}
		for i, column := range parent.Columns {
			target, exists := d.Index[column.Name]
			if !exists || target < 0 || target >= len(d.Attributes) {
				return invalid()
			}
			mapper.targetSlots[i] = target
		}
		for _, constraint := range parent.NotNullConstraints {
			if constraint.NoInherit {
				continue
			}
			sourceSlot := slices.Index(parent.Attributes, constraint.Attribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.Attributes[mapper.targetSlots[sourceSlot]]
			if !slices.ContainsFunc(graph.notNull[target].origins, func(origin inheritanceOrigin) bool {
				return origin.parent == parentAddress && origin.constraint == constraint.OID && origin.attribute == constraint.Attribute
			}) {
				return invalid()
			}
		}
		for _, constraint := range parent.CheckConstraints {
			if !constraint.NoInherit && !slices.ContainsFunc(graph.checks[constraint.Name].origins, func(origin inheritanceOrigin) bool {
				return origin.parent == parentAddress && origin.constraint == constraint.OID
			}) {
				return invalid()
			}
		}
		for sourceAttribute, generation := range parent.Generations {
			sourceSlot := slices.Index(parent.Attributes, sourceAttribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.Attributes[mapper.targetSlots[sourceSlot]]
			child, ok := d.Generations[target]
			if !ok || child.kind != generation.kind {
				return invalid()
			}
		}
	}
	for attribute, provenance := range graph.notNull {
		child := slices.IndexFunc(d.NotNullConstraints, func(c notNullConstraint) bool { return c.Attribute == attribute })
		if child < 0 || !provenance.local && provenance.count() == 0 {
			return invalid()
		}
		if err := validateRetiredInheritance(v, d, provenance, d.NotNullConstraints[child].OID, false); err != nil {
			return err
		}
		origins := make(map[ids.ObjectAddress]bool)
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.constraint == 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.MustRelation(origin.parent)
			found := slices.ContainsFunc(parent.NotNullConstraints, func(c notNullConstraint) bool {
				return c.OID == origin.constraint && c.Attribute == origin.attribute && c.OID != d.NotNullConstraints[child].OID
			})
			if !found {
				return invalid()
			}
			sourceSlot := slices.Index(parent.Attributes, origin.attribute)
			targetSlot := slices.Index(d.Attributes, attribute)
			if sourceSlot < 0 || targetSlot < 0 || parent.Columns[sourceSlot].Name != d.Columns[targetSlot].Name {
				return invalid()
			}
		}
	}
	for name, provenance := range graph.checks {
		child := slices.IndexFunc(d.CheckConstraints, func(c checkConstraint) bool { return c.Name == name })
		if child < 0 || !provenance.local && provenance.count() == 0 || d.CheckConstraints[child].NoInherit && provenance.count() != 0 {
			return invalid()
		}
		if err := validateRetiredInheritance(v, d, provenance, d.CheckConstraints[child].OID, true); err != nil {
			return err
		}
		origins := make(map[ids.ObjectAddress]bool)
		for _, origin := range provenance.origins {
			if !seen[origin.parent] || origins[origin.parent] || origin.attribute != 0 || origin.constraint == 0 {
				return invalid()
			}
			origins[origin.parent] = true
			parent := v.MustRelation(origin.parent)
			source := slices.IndexFunc(parent.CheckConstraints, func(c checkConstraint) bool {
				return c.OID == origin.constraint && c.Name == name && !c.NoInherit && c.OID != d.CheckConstraints[child].OID
			})
			if source < 0 {
				return invalid()
			}
			mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.Columns))}
			for i, c := range parent.Columns {
				mapper.targetSlots[i] = d.Index[c.Name]
			}
			node, err := mapper.remap(parent.CheckConstraints[source].expression, d, v)
			if err != nil || !sameInheritedExpression(node, d.CheckConstraints[child].expression, d, v) {
				return invalid()
			}
		}
	}
	for _, constraint := range d.NotNullConstraints {
		if _, ok := graph.notNull[constraint.Attribute]; !ok {
			return invalid()
		}
	}
	for _, constraint := range d.CheckConstraints {
		if _, ok := graph.checks[constraint.Name]; !ok {
			return invalid()
		}
	}
	// An explicit DFS validates imported/internal candidates as well as CREATE.
	visiting := make(map[ids.ObjectAddress]bool)
	visited := make(map[ids.ObjectAddress]bool)
	var visit func(*relationDescriptor) bool
	visit = func(current *relationDescriptor) bool {
		if visiting[current.address] {
			return false
		}
		if visited[current.address] {
			return true
		}
		visiting[current.address] = true
		if current.Inheritance != nil {
			for _, address := range current.Inheritance.parents {
				parent, ok := v.Relation(address)
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
