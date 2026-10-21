package table

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RenameInheritanceColumnPrecheck is the renamed root before the recursion:
// it rejects ONLY on a root with children.
func RenameInheritanceColumnPrecheck(view catalog.Read, root *catalog.Relation, column, destination string, only bool) (*catalog.Relation, error) {
	candidate, err := RenameColumnCandidate(root, column, destination)
	if err != nil {
		return nil, err
	}
	if only && catalog.InheritanceHasChildren(view, root.Address()) {
		return nil, sqlError("42P16", fmt.Sprintf(`inherited column "%s" must be renamed in child tables too`, column))
	}
	return candidate, nil
}

// RenameInheritanceColumn is the member old of a column rename with a new
// revision; candidate is the renamed root and selected the rename's members.
// A member's column must come only from selected parents.
func RenameInheritanceColumn(selected map[ids.ObjectAddress]bool, root, candidate, old *catalog.Relation, column, destination string) (*catalog.Relation, error) {
	next := candidate
	if old != root {
		slot, exists := old.Index[column]
		if !exists {
			return nil, ids.ErrCatalogReplaced
		}
		for _, origin := range old.Inheritance.Columns[old.Attributes[slot]].Origins {
			if !selected[origin.Parent] {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot rename inherited column "%s"`, column))
			}
		}
		var err error
		next, err = RenameColumnCandidate(old, column, destination)
		if err != nil {
			return nil, err
		}
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next.ObjectRevision = old.Revision() + 1
	return next, nil
}

// RenameInheritanceConstraintPrecheck rejects renaming an inherited constraint
// of root, or a recursing one under ONLY, and reports whether the rename
// recurses to the children.
func RenameInheritanceConstraintPrecheck(view catalog.Read, root *catalog.Relation, name string, kind byte, only bool) (bool, error) {
	inherited := false
	noInherit := false
	if root.Inheritance != nil {
		if kind == 'c' {
			inherited = root.Inheritance.Checks[name].Count() != 0
		} else {
			for _, c := range root.Constraints.NotNull {
				if c.Name == name {
					inherited = root.Inheritance.NotNull[c.Attribute].Count() != 0
				}
			}
		}
	}
	if inherited {
		return false, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
	}
	if kind == 'c' {
		for _, c := range root.Constraints.Check {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	} else {
		for _, c := range root.Constraints.NotNull {
			if c.Name == name {
				noInherit = c.NoInherit
			}
		}
	}
	if only && !noInherit && catalog.InheritanceHasChildren(view, root.Address()) {
		return false, sqlError("42P16", fmt.Sprintf(`inherited constraint "%s" must be renamed in child tables too`, name))
	}
	return !only && !noInherit, nil
}

// RenameInheritanceConstraint is the member old of a NOT NULL or CHECK rename;
// a member's constraint must come only from selected parents and must have no
// retired history.
func RenameInheritanceConstraint(selected map[ids.ObjectAddress]bool, root, old *catalog.Relation, name, destination string, kind byte) (*catalog.Relation, error) {
	if old != root {
		provenance := old.Inheritance.Checks[name]
		if kind == 'n' {
			for _, c := range old.Constraints.NotNull {
				if c.Name == name {
					provenance = old.Inheritance.NotNull[c.Attribute]
				}
			}
		}
		if len(provenance.Retired) != 0 {
			return nil, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
		}
		for _, origin := range provenance.Origins {
			if !selected[origin.Parent] {
				return nil, sqlError("42P16", fmt.Sprintf(`cannot rename inherited constraint "%s"`, name))
			}
		}
	}
	next, err := RenameLocalConstraint(old, name, destination, kind)
	if err != nil {
		return nil, err
	}
	if next.Inheritance != nil {
		next.Inheritance = catalog.CloneInheritance(next.Inheritance)
		if kind == 'c' {
			next.Inheritance.Checks[destination] = next.Inheritance.Checks[name]
			delete(next.Inheritance.Checks, name)
		}
	}
	return next, nil
}
