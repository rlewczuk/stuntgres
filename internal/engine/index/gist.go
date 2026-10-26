package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
)

// The shared extraction program owns exact keys, not an ordering. Neither
// logical method binds a comparator or sorts its membership.
func bindLogicalKeys(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if (d.Method != catalog.GistMethod && d.Method != catalog.SpgistMethod) || d.Unique || d.Method == catalog.SpgistMethod && len(d.Keys) != 1 {
		return Ordering{}, fmt.Errorf("stuntgres: invalid logical index definition")
	}
	o := Ordering{Slots: make([]int, len(d.Keys)), Keys: d.Keys}
	for i, key := range d.Keys {
		u, err := KeyUsage(parent, key)
		if err != nil {
			return o, err
		}
		if key.Class.MethodOID() != uint32(d.MethodIdentity()) {
			return o, fmt.Errorf("stuntgres: logical operator class method mismatch")
		}
		if _, err := routine.BindClassSupport(view, key.Class); err != nil {
			return o, err
		}
		admitted := d.Method == catalog.GistMethod && key.Class.IsGiST() || d.Method == catalog.SpgistMethod && key.Class.IsSPGiST()
		if !admitted || !key.Class.Accepts(u.Type) || key.Desc || key.NullsFirst {
			return o, fmt.Errorf("stuntgres: invalid logical index key")
		}
		if err := validateClassOptions(view, key); err != nil {
			return o, err
		}
		if key.Derived != nil {
			if key.Derived.Definition == nil || key.Derived.Execution == nil {
				return o, fmt.Errorf("stuntgres: missing logical index expression")
			}
			if o.Derived == nil {
				o.Derived = make([]expr.Expr, len(d.Keys))
			}
			o.Slots[i], o.Derived[i] = -1, key.Derived.Execution
		} else {
			o.Slots[i] = slices.Index(parent.Attributes, key.Attribute)
			if o.Slots[i] < 0 {
				return o, fmt.Errorf("stuntgres: missing logical index attribute")
			}
		}
	}
	return o, nil
}

func BindIndexKeys(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if d.Method == catalog.BrinMethod {
		bindings, err := BindBrinIndex(view, parent, d)
		if err != nil {
			return Ordering{}, err
		}
		return brinKeyProgram(bindings, d), nil
	}
	if d.Method == catalog.GistMethod || d.Method == catalog.SpgistMethod {
		return bindLogicalKeys(view, parent, d)
	}
	return BindIndexOrdering(view, parent, d)
}
