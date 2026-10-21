package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// view is the read the retired constraints are named against: the stage's
// base while staging, the definition base while preparing.
// retiredBase is the read inheritRetiredColumnConstraints names against.
func (state *inheritanceAlterState) retiredBase() catalog.Read {
	if state.stage != nil {
		return state.stage.Base
	}
	return state.definition.base
}

func (e *Engine) remapRetiredConstraints(next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	// Runtime certificates share the source catalog's namespace. On replacement,
	// archive that namespace once above every already imported namespace.
	var runtimeArchive uint64
	for _, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		for _, p := range table.Inheritance.NotNull {
			for _, history := range p.Retired {
				runtimeArchive = max(runtimeArchive, history.Archive)
			}
		}
		for _, p := range table.Inheritance.Checks {
			for _, history := range p.Retired {
				runtimeArchive = max(runtimeArchive, history.Archive)
			}
		}
	}
	if runtimeArchive == ^uint64(0) {
		return fmt.Errorf("stuntgres: retired inheritance namespace exhausted")
	}
	runtimeArchive++
	for address, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.Inheritance == nil {
			continue
		}
		namespace, ok := next.Namespace(table.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		has := false
		for _, p := range table.Inheritance.NotNull {
			has = has || len(p.Retired) != 0
		}
		for _, p := range table.Inheritance.Checks {
			has = has || len(p.Retired) != 0
		}
		if !has {
			continue
		}
		old, ok := previous[address].(*catalog.Relation)
		if !ok {
			return fmt.Errorf("stuntgres: missing retired constraint owner")
		}
		graph := catalog.CloneInheritance(table.Inheritance)
		remap := func(p catalog.InheritedConstraint, oldOID, newOID ids.OID) (catalog.InheritedConstraint, error) {
			p.Retired = slices.Clone(p.Retired)
			for i := range p.Retired {
				record := &p.Retired[i]
				if record.Own != oldOID || oldOID == 0 || newOID == 0 {
					return p, fmt.Errorf("stuntgres: missing retired constraint identity")
				}
				if record.Archive == 0 {
					record.Archive = runtimeArchive
				}
				record.Own = newOID
				record.Parent = e.catalog.Domain.Ref(record.Parent.Epoch, record.Parent.Address)
				record.Parent.Epoch = next.Epoch()
				record.Replayed = true
			}
			return p, nil
		}
		for attribute, p := range graph.NotNull {
			if len(p.Retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.Constraints.NotNull {
				if c.Attribute == attribute {
					before = c.OID
				}
			}
			for _, c := range table.Constraints.NotNull {
				if c.Attribute == attribute {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.NotNull[attribute] = replacement
		}
		for name, p := range graph.Checks {
			if len(p.Retired) == 0 {
				continue
			}
			var before, after ids.OID
			for _, c := range old.Constraints.Check {
				if c.Name == name {
					before = c.OID
				}
			}
			for _, c := range table.Constraints.Check {
				if c.Name == name {
					after = c.OID
				}
			}
			replacement, err := remap(p, before, after)
			if err != nil {
				return err
			}
			graph.Checks[name] = replacement
		}
		copy := *table
		copy.Inheritance = graph
		next.SetDescriptor(address, &copy)
	}
	return nil
}
