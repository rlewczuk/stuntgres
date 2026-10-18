package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
)

// The shared extraction program owns keys, not an ordering. No comparator is
// bound for logical GiST membership and callers must never sort these entries.
func bindLogicalExclusionKeys(parent *relationDescriptor, d *indexDefinition) (indexOrdering, error) {
	if d.Method != gistMethod || d.Unique {
		return indexOrdering{}, fmt.Errorf("stuntgres: invalid logical GiST definition")
	}
	o := indexOrdering{slots: make([]int, len(d.Keys)), keys: d.Keys}
	for i, key := range d.Keys {
		u, err := indexKeyUsage(parent, key)
		if err != nil {
			return o, err
		}
		if !key.Class.IsGiST() || !key.Class.Accepts(u.Type) || key.Desc || key.NullsFirst {
			return o, fmt.Errorf("stuntgres: invalid logical GiST key")
		}
		if key.Derived != nil {
			if key.Derived.Definition == nil || key.Derived.Execution == nil {
				return o, fmt.Errorf("stuntgres: missing logical GiST expression")
			}
			if o.derived == nil {
				o.derived = make([]expr.Expr, len(d.Keys))
			}
			o.slots[i], o.derived[i] = -1, key.Derived.Execution
		} else {
			o.slots[i] = slices.Index(parent.Attributes, key.Attribute)
			if o.slots[i] < 0 {
				return o, fmt.Errorf("stuntgres: missing logical GiST attribute")
			}
		}
	}
	return o, nil
}

func bindIndexKeys(parent *relationDescriptor, d *indexDefinition) (indexOrdering, error) {
	if d.Method == gistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	return bindIndexOrdering(parent, d)
}

// Comparator-based exclusive claims are sound only for the original equality
// providers. Overlap admissions use existing dirty projections/completion waits.
func (d *indexDefinition) exactEqualityClaims() bool {
	if d.Method == gistMethod {
		return false
	}
	for _, operator := range d.Exclusion {
		if !operator.IsEquality() {
			return false
		}
	}
	return true
}
