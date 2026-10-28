package index

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// SelectConstraintName resolves one SET CONSTRAINTS name, label, along the
// namespaces of path, stopping at the first namespace with a table
// constraint of that name. It adds eligible UNIQUE, PK, EX and FK identities
// to selected. Naming a nondeferrable constraint with deferred is
// 42809; a name found nowhere is 42704.
func SelectConstraintName(view catalog.Read, path analyze.NamespaceSelection, label string, deferred bool, selected map[ids.OID]bool) error {
	for i := range path.Count() {
		namespace := path.At(i)
		matched := false
		for _, descriptor := range view.Descriptors() {
			parent, ok := descriptor.(*catalog.Relation)
			if !ok || parent.Namespace() != namespace || parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.PartitionedRelation {
				continue
			}
			for _, c := range parent.Constraints.Exclusion {
				if c.Name != label {
					continue
				}
				matched = true
				if deferred && !c.Deferrable {
					return sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
				}
				if c.Deferrable {
					selected[c.OID] = true
				}
			}
			nondeferrable := false
			for _, c := range parent.Constraints.Unique {
				if c.Name != label {
					continue
				}
				matched = true
				if deferred && !c.Deferrable {
					return sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
				}
				if c.Deferrable {
					selected[c.OID] = true
				}
			}
			for _, c := range parent.Constraints.ForeignKeys {
				if c.Name != label {
					continue
				}
				matched = true
				if deferred && !c.Deferrable {
					return sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
				}
				if c.Deferrable {
					selected[c.OID] = true
				}
			}
			for _, c := range parent.Constraints.Check {
				if c.Name == label {
					nondeferrable = true
				}
			}
			for _, c := range parent.Constraints.NotNull {
				if c.Name == label {
					nondeferrable = true
				}
			}
			if nondeferrable {
				matched = true
				if deferred {
					return sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
				}
			}
		}
		if matched {
			return nil
		}
	}
	return sqlError("42704", fmt.Sprintf(`constraint "%s" does not exist`, label))
}

// SelectInheritedConstraints extends selection through actual index-backed
// parent identities, never unrelated same-name locals or ordinary FK inheritance.
// Catalog iteration has no tree order, so it repeats until nothing changes.
func SelectInheritedConstraints(view catalog.Read, selected map[ids.OID]bool) {
	for changed := true; changed; {
		changed = false
		for _, descriptor := range view.Descriptors() {
			parent, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, c := range parent.Constraints.Exclusion {
				if c.Parent != 0 && selected[c.Parent] && !selected[c.OID] {
					selected[c.OID] = true
					changed = true
				}
			}
			for _, c := range parent.Constraints.Unique {
				if c.Parent != 0 && selected[c.Parent] && !selected[c.OID] {
					selected[c.OID] = true
					changed = true
				}
			}
		}
	}
}
