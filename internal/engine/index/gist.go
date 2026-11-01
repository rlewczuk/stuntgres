package index

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
)

// The shared extraction program owns exact keys, not an ordering. These
// logical methods bind no comparator and never sort their memberships.
func bindLogicalKeys(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if (d.Method != catalog.GistMethod && d.Method != catalog.SpgistMethod && d.Method != catalog.BloomMethod) || d.Unique || d.Method == catalog.SpgistMethod && len(d.Keys) != 1 {
		return Ordering{}, fmt.Errorf("stuntgres: invalid logical index definition")
	}
	o := Ordering{Slots: make([]int, len(d.Keys)), Keys: d.Keys}
	keysOwned := false
	for i, key := range d.Keys {
		u, err := KeyUsage(parent, key)
		if err != nil {
			return o, err
		}
		if key.Class.MethodOID() != uint32(d.MethodIdentity()) {
			return o, fmt.Errorf("stuntgres: logical operator class method mismatch")
		}
		class, err := routine.BindClassSupport(view, key.Class)
		if err != nil {
			return o, err
		}
		if d.Method == catalog.BloomMethod && class != key.Class {
			if !keysOwned {
				o.Keys = slices.Clone(d.Keys)
				keysOwned = true
			}
			o.Keys[i].Class = class
		}
		admitted := d.Method == catalog.GistMethod && key.Class.IsGiST() || d.Method == catalog.SpgistMethod && key.Class.IsSPGiST() || d.Method == catalog.BloomMethod && key.Class.IsBloom()
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
	if d.Method == catalog.GistMethod || d.Method == catalog.SpgistMethod || d.Method == catalog.BloomMethod {
		return bindLogicalKeys(view, parent, d)
	}
	return BindIndexOrdering(view, parent, d)
}
