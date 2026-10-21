package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Copy only graph metadata. Stable attributes are relation-local and do not
// change when a whole relation address is remapped.
func RemapInheritance(graph *catalog.TableInheritance, address func(ids.ObjectAddress) ids.ObjectAddress, constraint func(catalog.InheritanceOrigin) ids.OID) *catalog.TableInheritance {
	if graph == nil {
		return nil
	}
	next := &catalog.TableInheritance{Parents: make([]ids.ObjectAddress, len(graph.Parents)), Columns: make(map[int32]catalog.InheritedColumn, len(graph.Columns)), Checks: make(map[string]catalog.InheritedConstraint, len(graph.Checks)), NotNull: make(map[int32]catalog.InheritedConstraint, len(graph.NotNull))}
	origins := func(source []catalog.InheritanceOrigin) []catalog.InheritanceOrigin {
		result := make([]catalog.InheritanceOrigin, len(source))
		for i, origin := range source {
			result[i] = origin
			result[i].Constraint = constraint(origin)
			result[i].Parent = address(origin.Parent)
		}
		return result
	}
	retired := func(source []catalog.RetiredInheritanceOrigin) []catalog.RetiredInheritanceOrigin {
		result := slices.Clone(source)
		for i := range result {
			result[i].Parent.Address = address(result[i].Parent.Address)
		}
		return result
	}
	for i, parent := range graph.Parents {
		next.Parents[i] = address(parent)
	}
	for attribute, column := range graph.Columns {
		column.Origins = origins(column.Origins)
		next.Columns[attribute] = column
	}
	for name, check := range graph.Checks {
		check.Origins = origins(check.Origins)
		check.Retired = retired(check.Retired)
		next.Checks[name] = check
	}
	for attribute, notNull := range graph.NotNull {
		notNull.Origins = origins(notNull.Origins)
		notNull.Retired = retired(notNull.Retired)
		next.NotNull[attribute] = notNull
	}
	return next
}

// Definition identities can collide independently of relation addresses. Use
// each parent's captured identity, never a global old-OID map (TEMP and imported
// constraints can have the same old number).
func RemapInheritanceConstraintOrigins(next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	for address, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		if namespace, ok := next.Namespace(table.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		var remapErr error
		graph := RemapInheritance(table.Inheritance, func(a ids.ObjectAddress) ids.ObjectAddress { return a }, func(origin catalog.InheritanceOrigin) ids.OID {
			if origin.Constraint == 0 {
				return 0
			}
			old, ok := previous[origin.Parent].(*catalog.Relation)
			parent, exists := next.Relation(origin.Parent)
			if !ok || !exists {
				remapErr = fmt.Errorf("stuntgres: missing inheritance constraint parent")
				return origin.Constraint
			}
			for _, check := range old.Constraints.Check {
				if check.OID == origin.Constraint {
					for _, current := range parent.Constraints.Check {
						if current.Name == check.Name {
							return current.OID
						}
					}
				}
			}
			for _, notNull := range old.Constraints.NotNull {
				if notNull.OID == origin.Constraint {
					for _, current := range parent.Constraints.NotNull {
						if current.Attribute == notNull.Attribute {
							return current.OID
						}
					}
				}
			}
			remapErr = fmt.Errorf("stuntgres: missing inheritance constraint origin")
			return origin.Constraint
		})
		if remapErr != nil {
			return remapErr
		}
		copy := *table
		copy.Inheritance = graph
		next.SetDescriptor(address, &copy)
	}
	return nil
}
