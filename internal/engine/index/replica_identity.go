package index

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ReplicaIdentityEligibility checks only released row-identity requirements.
// Constraint timing comes from the actual unique/exclusion provider. Validity
// and INCLUDE nullability add no restrictions; publication validates structure.
func ReplicaIdentityEligibility(parent, idx *catalog.Relation) error {
	d, name := idx.SQLIndex, idx.Name()
	if d.Parent != parent.Address() {
		return sqlError("42809", fmt.Sprintf(`"%s" is not an index for table "%s"`, name, parent.Name()))
	}
	if !d.Unique {
		return sqlError("42809", fmt.Sprintf(`cannot use non-unique index "%s" as replica identity`, name))
	}
	if _, deferrable, _, _ := IndexConstraintTiming(parent, idx); deferrable {
		return sqlError("0A000", fmt.Sprintf(`cannot use non-immediate index "%s" as replica identity`, name))
	}
	for _, key := range d.Keys {
		if key.Derived != nil {
			return sqlError("0A000", fmt.Sprintf(`cannot use expression index "%s" as replica identity`, name))
		}
	}
	if d.Predicate != nil {
		return sqlError("0A000", fmt.Sprintf(`cannot use partial index "%s" as replica identity`, name))
	}
	for _, key := range d.Keys {
		if key.Attribute <= 0 {
			return sqlError("42P10", fmt.Sprintf(`index "%s" cannot be used as replica identity because column %d is a system column`, name, key.Attribute))
		}
		slot := parent.IndexForAttribute(key.Attribute)
		if slot < 0 {
			return ids.ErrCatalogReplaced
		}
		if parent.Columns[slot].Nullable {
			return sqlError("42809", fmt.Sprintf(`index "%s" cannot be used as replica identity because column "%s" is nullable`, name, parent.Columns[slot].Name))
		}
	}
	return nil
}
