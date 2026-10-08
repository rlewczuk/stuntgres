package engine

import (
	"fmt"
	"sort"
)

// structuralNull only recognizes transparent coercions of a literal NULL.
// Other expressions remain definitions even when evaluation would return NULL.
func structuralNull(node BoundExpr) bool {
	for {
		switch n := node.(type) {
		case literalExpression:
			return n.value.IsNull()
		case coercionExpression:
			node = n.child
		default:
			return false
		}
	}
}

func (e *Engine) assignDefinitionOIDsLocked(d *relationDescriptor) error {
	for i := range d.notNullConstraints {
		if d.notNullConstraints[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.notNullConstraints[i].OID = id
		}
	}
	for i := range d.checkConstraints {
		if d.checkConstraints[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.checkConstraints[i].OID = id
		}
	}
	for attr, def := range d.defaults {
		if def.OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			def.OID = id
			d.defaults[attr] = def
		}
	}
	return nil
}

func definitionOIDUsed(d *relationDescriptor, id OID) bool {
	if d.view != nil && d.view.rule == id {
		return true
	}
	for _, c := range d.notNullConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.checkConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.defaults {
		if c.OID == id {
			return true
		}
	}
	return false
}

func validateDefinitionOIDs(view CatalogRead) error {
	used := make(map[OID]bool)
	for address := range view.state.descriptors {
		used[address.ObjectID] = true
	}
	claim := func(id OID) error {
		if id == 0 || used[id] {
			return fmt.Errorf("stuntgres: invalid stored definition identity")
		}
		used[id] = true
		return nil
	}
	for _, descriptor := range view.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if d.view != nil {
			if err := claim(d.view.rule); err != nil {
				return err
			}
		}
		for _, c := range d.notNullConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.checkConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.defaults {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Replacement retains temporary objects and imports persistent definitions.
// Reserve the entire candidate before allocating collision replacements.
func (e *Engine) remapDefinitionOIDsLocked(next *catalogState) error {
	reserved := make(map[OID]bool)
	var relations []*relationDescriptor
	for address, descriptor := range next.descriptors {
		reserved[address.ObjectID] = true
		if d, ok := descriptor.(*relationDescriptor); ok {
			relations = append(relations, d)
			if d.view != nil {
				reserved[d.view.rule] = true
			}
			for _, c := range d.notNullConstraints {
				reserved[c.OID] = true
			}
			for _, c := range d.checkConstraints {
				reserved[c.OID] = true
			}
			for _, c := range d.defaults {
				reserved[c.OID] = true
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	claimed := make(map[OID]bool)
	for address := range next.descriptors {
		claimed[address.ObjectID] = true
	}
	for _, d := range relations {
		if d.view != nil {
			claimed[d.view.rule] = true
		}
	}
	remint := func(id OID) (OID, error) {
		if id == 0 {
			return 0, fmt.Errorf("stuntgres: invalid imported definition identity")
		}
		if !claimed[id] {
			claimed[id] = true
			return id, nil
		}
		for {
			candidate, err := e.allocateOIDLocked()
			if err != nil {
				return 0, err
			}
			if !reserved[candidate] {
				reserved[candidate], claimed[candidate] = true, true
				return candidate, nil
			}
		}
	}
	for _, old := range relations {
		if len(old.notNullConstraints)+len(old.checkConstraints)+len(old.defaults) == 0 {
			continue
		}
		d := *old
		changed, nullCopied, checkCopied, defaultCopied := false, false, false, false
		for i, def := range old.notNullConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !nullCopied {
				d.notNullConstraints = append([]notNullConstraint(nil), old.notNullConstraints...)
				nullCopied = true
			}
			d.notNullConstraints[i].OID = id
			changed = true
		}
		for i, def := range old.checkConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !checkCopied {
				d.checkConstraints = append([]checkConstraint(nil), old.checkConstraints...)
				checkCopied = true
			}
			d.checkConstraints[i].OID = id
			changed = true
		}
		for attr, def := range old.defaults {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !defaultCopied {
				d.defaults = make(map[int32]columnDefault, len(old.defaults))
				for key, value := range old.defaults {
					d.defaults[key] = value
				}
				defaultCopied = true
			}
			def.OID = id
			d.defaults[attr] = def
			changed = true
		}
		if changed {
			replacement := d
			next.descriptors[d.address] = &replacement
		}
	}
	return nil
}
