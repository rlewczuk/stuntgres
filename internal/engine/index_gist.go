package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
)

// The shared extraction program owns keys, not an ordering. No comparator is
// bound for logical GiST membership and callers must never sort these entries.
func bindLogicalExclusionKeys(parent *relationDescriptor, d *indexDefinition) (indexOrdering, error) {
	if d.method != gistMethod || d.unique {
		return indexOrdering{}, fmt.Errorf("stuntgres: invalid logical GiST definition")
	}
	o := indexOrdering{slots: make([]int, len(d.keys)), keys: d.keys}
	for i, key := range d.keys {
		u, err := indexKeyUsage(parent, key)
		if err != nil {
			return o, err
		}
		if !key.class.IsGiST() || !key.class.Accepts(u.Type) || key.desc || key.nullsFirst {
			return o, fmt.Errorf("stuntgres: invalid logical GiST key")
		}
		if key.derived != nil {
			if key.derived.definition == nil || key.derived.execution == nil {
				return o, fmt.Errorf("stuntgres: missing logical GiST expression")
			}
			if o.derived == nil {
				o.derived = make([]expr.Expr, len(d.keys))
			}
			o.slots[i], o.derived[i] = -1, key.derived.execution
		} else {
			o.slots[i] = slices.Index(parent.Attributes, key.attribute)
			if o.slots[i] < 0 {
				return o, fmt.Errorf("stuntgres: missing logical GiST attribute")
			}
		}
	}
	return o, nil
}

func bindIndexKeys(parent *relationDescriptor, d *indexDefinition) (indexOrdering, error) {
	if d.method == gistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	return bindIndexOrdering(parent, d)
}

// Comparator-based exclusive claims are sound only for the original equality
// providers. Overlap admissions use existing dirty projections/completion waits.
func (d *indexDefinition) exactEqualityClaims() bool {
	if d.method == gistMethod {
		return false
	}
	for _, operator := range d.exclusion {
		if !operator.IsEquality() {
			return false
		}
	}
	return true
}
