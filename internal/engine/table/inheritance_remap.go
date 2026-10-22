package table

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
	next := &catalog.TableInheritance{Parents: make([]catalog.InheritanceParent, len(graph.Parents)), Columns: make(map[int32]catalog.InheritedColumn, len(graph.Columns)), Checks: make(map[string]catalog.InheritedConstraint, len(graph.Checks)), NotNull: make(map[int32]catalog.InheritedConstraint, len(graph.NotNull))}
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
			if !result[i].Detached {
				result[i].Parent.Address = address(result[i].Parent.Address)
			}
		}
		return result
	}
	for i, parent := range graph.Parents {
		next.Parents[i] = catalog.InheritanceParent{Relation: address(parent.Relation), Seqno: parent.Seqno}
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
		if !ok || table.Inheritance == nil && !HasPartitionConstraintOrigins(table) {
			continue
		}
		if namespace, ok := next.Namespace(table.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		var remapErr error
		remapConstraint := func(origin catalog.InheritanceOrigin) ids.OID {
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
		}
		copy := *table
		copy.Inheritance = RemapInheritance(table.Inheritance, func(a ids.ObjectAddress) ids.ObjectAddress { return a }, remapConstraint)
		RemapPartitionConstraintOrigins(&copy, func(a ids.ObjectAddress) ids.ObjectAddress { return a }, remapConstraint)
		if remapErr != nil {
			return remapErr
		}
		next.SetDescriptor(address, &copy)
	}
	return nil
}

func HasPartitionConstraintOrigins(d *catalog.Relation) bool {
	for _, c := range d.Constraints.Check {
		if c.PartitionOrigin != nil {
			return true
		}
	}
	for _, c := range d.Constraints.NotNull {
		if c.PartitionOrigin != nil {
			return true
		}
	}
	return false
}

// RemapPartitionConstraintOrigins uses the same parent-qualified identity
// callbacks as ordinary inheritance. Stable attributes remain relation-local.
func RemapPartitionConstraintOrigins(d *catalog.Relation, address func(ids.ObjectAddress) ids.ObjectAddress, constraint func(catalog.InheritanceOrigin) ids.OID) {
	remap := func(source *catalog.InheritedConstraint) *catalog.InheritedConstraint {
		next := catalog.CloneConstraintOrigin(source)
		if next == nil {
			return nil
		}
		for i, origin := range next.Origins {
			next.Origins[i].Constraint = constraint(origin)
			next.Origins[i].Parent = address(origin.Parent)
		}
		return next
	}
	if slices.ContainsFunc(d.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.PartitionOrigin != nil }) {
		d.Constraints.Check = slices.Clone(d.Constraints.Check)
		for i := range d.Constraints.Check {
			d.Constraints.Check[i].PartitionOrigin = remap(d.Constraints.Check[i].PartitionOrigin)
		}
	}
	if slices.ContainsFunc(d.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.PartitionOrigin != nil }) {
		d.Constraints.NotNull = slices.Clone(d.Constraints.NotNull)
		for i := range d.Constraints.NotNull {
			d.Constraints.NotNull[i].PartitionOrigin = remap(d.Constraints.NotNull[i].PartitionOrigin)
		}
	}
}
