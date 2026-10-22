package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
)

// The shared extraction program owns keys, not an ordering. No comparator is
// bound for logical GiST membership and callers must never sort these entries.
func bindLogicalExclusionKeys(parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if d.Method != catalog.GistMethod || d.Unique {
		return Ordering{}, fmt.Errorf("stuntgres: invalid logical GiST definition")
	}
	o := Ordering{Slots: make([]int, len(d.Keys)), Keys: d.Keys}
	for i, key := range d.Keys {
		u, err := KeyUsage(parent, key)
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
			if o.Derived == nil {
				o.Derived = make([]expr.Expr, len(d.Keys))
			}
			o.Slots[i], o.Derived[i] = -1, key.Derived.Execution
		} else {
			o.Slots[i] = slices.Index(parent.Attributes, key.Attribute)
			if o.Slots[i] < 0 {
				return o, fmt.Errorf("stuntgres: missing logical GiST attribute")
			}
		}
	}
	return o, nil
}

func BindIndexKeys(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if d.Method == catalog.GistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	return BindIndexOrdering(view, parent, d)
}
