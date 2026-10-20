package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Retained TEMP definitions keep their original row-reference epoch across a
// persistent replacement. Compare at current row authority without changing
// either descriptor or remapping external provider references.
func sameInheritedExpression(mapped, child expr.Expr, d *catalog.Relation, v catalog.Read) bool {
	if SameGroupExpression(mapped, child) {
		return true
	}
	identity := createLikeInput{Source: d}
	current, err := remapLikeExpression(&identity, child, d, v)
	return err == nil && SameGroupExpression(mapped, current)
}

func validateInheritance(v catalog.Read, d *catalog.Relation) error {
	graph := d.Inheritance
	if graph == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid table inheritance graph") }
	if d.RelationKind != catalog.TableRelation || len(graph.Parents) == 0 || len(graph.Columns) != len(d.Columns) {
		return invalid()
	}
	childNS, ok := v.Namespace(d.Namespace())
	if !ok {
		return invalid()
	}
	seen := make(map[ids.ObjectAddress]bool, len(graph.Parents))
	for _, address := range graph.Parents {
		parent, ok := v.Relation(address)
		if !ok || parent.Address() != address || parent.RelationKind != catalog.TableRelation || address.SubID != 0 || seen[address] || address == d.Address() {
			return invalid()
		}
		seen[address] = true
		parentNS, ok := v.Namespace(parent.Namespace())
		if !ok || parentNS.Temporary != (ids.SessionToken{}) && parentNS.Temporary != childNS.Temporary {
			return invalid()
		}
	}
	for slot, attribute := range d.Attributes {
		provenance, ok := graph.Columns[attribute]
		if !ok || !provenance.Local && len(provenance.Origins) == 0 {
			return invalid()
		}
		origins := make(map[ids.ObjectAddress]bool, len(provenance.Origins))
		for _, origin := range provenance.Origins {
			if !seen[origin.Parent] || origins[origin.Parent] || origin.Constraint != 0 {
				return invalid()
			}
			origins[origin.Parent] = true
			parent := v.MustRelation(origin.Parent)
			sourceSlot := slices.Index(parent.Attributes, origin.Attribute)
			if sourceSlot < 0 || parent.Columns[sourceSlot].Name != d.Columns[slot].Name || analyze.InheritanceColumnConflict(v, parent.Columns[sourceSlot], d.Columns[slot], false) != nil {
				return invalid()
			}
		}
		for _, parentAddress := range graph.Parents {
			parent := v.MustRelation(parentAddress)
			if _, ok := parent.Index[d.Columns[slot].Name]; ok && !origins[parentAddress] {
				return invalid()
			}
		}
	}
	for _, parentAddress := range graph.Parents {
		parent := v.MustRelation(parentAddress)
		mapper := createLikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
		for i, column := range parent.Columns {
			target, exists := d.Index[column.Name]
			if !exists || target < 0 || target >= len(d.Attributes) {
				return invalid()
			}
			mapper.TargetSlots[i] = target
		}
		for _, constraint := range parent.Constraints.NotNull {
			if constraint.NoInherit {
				continue
			}
			sourceSlot := slices.Index(parent.Attributes, constraint.Attribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.Attributes[mapper.TargetSlots[sourceSlot]]
			if !slices.ContainsFunc(graph.NotNull[target].Origins, func(origin catalog.InheritanceOrigin) bool {
				return origin.Parent == parentAddress && origin.Constraint == constraint.OID && origin.Attribute == constraint.Attribute
			}) {
				return invalid()
			}
		}
		for _, constraint := range parent.Constraints.Check {
			if !constraint.NoInherit && !slices.ContainsFunc(graph.Checks[constraint.Name].Origins, func(origin catalog.InheritanceOrigin) bool {
				return origin.Parent == parentAddress && origin.Constraint == constraint.OID
			}) {
				return invalid()
			}
		}
		for sourceAttribute, generation := range parent.ColumnMeta.Generations {
			sourceSlot := slices.Index(parent.Attributes, sourceAttribute)
			if sourceSlot < 0 {
				return invalid()
			}
			target := d.Attributes[mapper.TargetSlots[sourceSlot]]
			child, ok := d.ColumnMeta.Generations[target]
			if !ok || child.Kind != generation.Kind {
				return invalid()
			}
		}
	}
	for attribute, provenance := range graph.NotNull {
		child := slices.IndexFunc(d.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute })
		if child < 0 || !provenance.Local && provenance.Count() == 0 {
			return invalid()
		}
		if err := validateRetiredInheritance(v, d, provenance, d.Constraints.NotNull[child].OID, false); err != nil {
			return err
		}
		origins := make(map[ids.ObjectAddress]bool)
		for _, origin := range provenance.Origins {
			if !seen[origin.Parent] || origins[origin.Parent] || origin.Constraint == 0 {
				return invalid()
			}
			origins[origin.Parent] = true
			parent := v.MustRelation(origin.Parent)
			found := slices.ContainsFunc(parent.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
				return c.OID == origin.Constraint && c.Attribute == origin.Attribute && c.OID != d.Constraints.NotNull[child].OID
			})
			if !found {
				return invalid()
			}
			sourceSlot := slices.Index(parent.Attributes, origin.Attribute)
			targetSlot := slices.Index(d.Attributes, attribute)
			if sourceSlot < 0 || targetSlot < 0 || parent.Columns[sourceSlot].Name != d.Columns[targetSlot].Name {
				return invalid()
			}
		}
	}
	for name, provenance := range graph.Checks {
		child := slices.IndexFunc(d.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name })
		if child < 0 || !provenance.Local && provenance.Count() == 0 || d.Constraints.Check[child].NoInherit && provenance.Count() != 0 {
			return invalid()
		}
		if err := validateRetiredInheritance(v, d, provenance, d.Constraints.Check[child].OID, true); err != nil {
			return err
		}
		origins := make(map[ids.ObjectAddress]bool)
		for _, origin := range provenance.Origins {
			if !seen[origin.Parent] || origins[origin.Parent] || origin.Attribute != 0 || origin.Constraint == 0 {
				return invalid()
			}
			origins[origin.Parent] = true
			parent := v.MustRelation(origin.Parent)
			source := slices.IndexFunc(parent.Constraints.Check, func(c catalog.CheckConstraint) bool {
				return c.OID == origin.Constraint && c.Name == name && !c.NoInherit && c.OID != d.Constraints.Check[child].OID
			})
			if source < 0 {
				return invalid()
			}
			mapper := createLikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
			for i, c := range parent.Columns {
				mapper.TargetSlots[i] = d.Index[c.Name]
			}
			node, err := remapLikeExpression(&mapper, parent.Constraints.Check[source].Expression, d, v)
			if err != nil || !sameInheritedExpression(node, d.Constraints.Check[child].Expression, d, v) {
				return invalid()
			}
		}
	}
	for _, constraint := range d.Constraints.NotNull {
		if _, ok := graph.NotNull[constraint.Attribute]; !ok {
			return invalid()
		}
	}
	for _, constraint := range d.Constraints.Check {
		if _, ok := graph.Checks[constraint.Name]; !ok {
			return invalid()
		}
	}
	// An explicit DFS validates imported/internal candidates as well as CREATE.
	visiting := make(map[ids.ObjectAddress]bool)
	visited := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) bool
	visit = func(current *catalog.Relation) bool {
		if visiting[current.Address()] {
			return false
		}
		if visited[current.Address()] {
			return true
		}
		visiting[current.Address()] = true
		if current.Inheritance != nil {
			for _, address := range current.Inheritance.Parents {
				parent, ok := v.Relation(address)
				if !ok || !visit(parent) {
					return false
				}
			}
		}
		delete(visiting, current.Address())
		visited[current.Address()] = true
		return true
	}
	if !visit(d) {
		return invalid()
	}
	return nil
}
