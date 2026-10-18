package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
)

const temporaryInheritanceReplacementError = "stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported"

// Called under Engine.mu at preallocation and final replacement admission.
// Retained TEMP relations cannot substitute persistent inheritance or OF sources.
func (e *Engine) checkTemporaryRelationSourcesReplacementLocked() error {
	for _, descriptor := range e.catalog.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.inheritance == nil {
			continue
		}
		namespace := e.catalog.state.descriptors[table.namespace].(*namespaceDescriptor)
		if namespace.temporary == (ids.SessionToken{}) {
			continue
		}
		for _, parent := range table.inheritance.parents {
			source, ok := e.catalog.state.descriptors[parent].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing inheritance parent")
			}
			if e.catalog.state.descriptors[source.namespace].(*namespaceDescriptor).temporary == (ids.SessionToken{}) {
				return fmt.Errorf("%s", temporaryInheritanceReplacementError)
			}
		}
	}
	return e.checkTemporaryTypedReplacementLocked()
}

// Copy only graph metadata. Stable attributes are relation-local and do not
// change when a whole relation address is remapped.
func remapInheritance(graph *tableInheritance, address func(ids.ObjectAddress) ids.ObjectAddress, constraint func(inheritanceOrigin) ids.OID) *tableInheritance {
	if graph == nil {
		return nil
	}
	next := &tableInheritance{parents: make([]ids.ObjectAddress, len(graph.parents)), columns: make(map[int32]inheritedColumn, len(graph.columns)), checks: make(map[string]inheritedConstraint, len(graph.checks)), notNull: make(map[int32]inheritedConstraint, len(graph.notNull))}
	origins := func(source []inheritanceOrigin) []inheritanceOrigin {
		result := make([]inheritanceOrigin, len(source))
		for i, origin := range source {
			result[i] = origin
			result[i].constraint = constraint(origin)
			result[i].parent = address(origin.parent)
		}
		return result
	}
	retired := func(source []retiredInheritanceOrigin) []retiredInheritanceOrigin {
		result := slices.Clone(source)
		for i := range result {
			result[i].parent.Address = address(result[i].parent.Address)
		}
		return result
	}
	for i, parent := range graph.parents {
		next.parents[i] = address(parent)
	}
	for attribute, column := range graph.columns {
		column.origins = origins(column.origins)
		next.columns[attribute] = column
	}
	for name, check := range graph.checks {
		check.origins = origins(check.origins)
		check.retired = retired(check.retired)
		next.checks[name] = check
	}
	for attribute, notNull := range graph.notNull {
		notNull.origins = origins(notNull.origins)
		notNull.retired = retired(notNull.retired)
		next.notNull[attribute] = notNull
	}
	return next
}

// Definition identities can collide independently of relation addresses. Use
// each parent's captured identity, never a global old-OID map (TEMP and imported
// constraints can have the same old number).
func remapInheritanceConstraintOrigins(next *catalogState, previous map[ids.ObjectAddress]Descriptor) error {
	for address, descriptor := range next.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || table.inheritance == nil {
			continue
		}
		if namespace, ok := next.descriptors[table.namespace].(*namespaceDescriptor); ok && namespace.temporary != (ids.SessionToken{}) {
			continue
		}
		var remapErr error
		graph := remapInheritance(table.inheritance, func(a ids.ObjectAddress) ids.ObjectAddress { return a }, func(origin inheritanceOrigin) ids.OID {
			if origin.constraint == 0 {
				return 0
			}
			old, ok := previous[origin.parent].(*relationDescriptor)
			parent, exists := next.descriptors[origin.parent].(*relationDescriptor)
			if !ok || !exists {
				remapErr = fmt.Errorf("stuntgres: missing inheritance constraint parent")
				return origin.constraint
			}
			for _, check := range old.checkConstraints {
				if check.OID == origin.constraint {
					for _, current := range parent.checkConstraints {
						if current.Name == check.Name {
							return current.OID
						}
					}
				}
			}
			for _, notNull := range old.notNullConstraints {
				if notNull.OID == origin.constraint {
					for _, current := range parent.notNullConstraints {
						if current.Attribute == notNull.Attribute {
							return current.OID
						}
					}
				}
			}
			remapErr = fmt.Errorf("stuntgres: missing inheritance constraint origin")
			return origin.constraint
		})
		if remapErr != nil {
			return remapErr
		}
		copy := *table
		copy.inheritance = graph
		next.descriptors[address] = &copy
	}
	return nil
}
