package table

import (
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// retiredHighWater is the highest retired-origin archive any relation of
// descriptors records.
func retiredHighWater(descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) uint64 {
	var maximum uint64
	for _, descriptor := range descriptors {
		if tbl, ok := descriptor.(*catalog.Relation); ok && tbl.Inheritance != nil {
			for _, p := range tbl.Inheritance.NotNull {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
			for _, p := range tbl.Inheritance.Checks {
				for _, history := range p.Retired {
					maximum = max(maximum, history.Archive)
				}
			}
		}
	}
	return maximum
}

// RetiredArchiveHighWater is the highest retired-origin archive in view;
// Restore numbers imported archives above it.
func RetiredArchiveHighWater(view catalog.Read) uint64 {
	return retiredHighWater(view.Descriptors())
}

// RemapRetiredConstraints renumbers the retired origins of next, a catalog
// replacing previous: each permanent relation's records follow its
// constraints' new OIDs, take next's epoch and parents under domain, and
// runtime records get one archive above every imported one.
func RemapRetiredConstraints(domain *ids.Domain, next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	// Runtime certificates share the source catalog's namespace. On replacement,
	// archive that namespace once above every already imported namespace.
	runtimeArchive := retiredHighWater(next.Descriptors())
	if runtimeArchive == ^uint64(0) {
		return fmt.Errorf("stuntgres: retired inheritance namespace exhausted")
	}
	runtimeArchive++
	for address, descriptor := range next.Descriptors() {
		tbl, ok := descriptor.(*catalog.Relation)
		if !ok || tbl.Inheritance == nil {
			continue
		}
		namespace, ok := next.Namespace(tbl.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		has := false
		for _, p := range tbl.Inheritance.NotNull {
			has = has || len(p.Retired) != 0
		}
		for _, p := range tbl.Inheritance.Checks {
			has = has || len(p.Retired) != 0
		}
		if !has {
			continue
		}
		old, ok := previous[address].(*catalog.Relation)
		if !ok {
			return fmt.Errorf("stuntgres: missing retired constraint owner")
		}
		graph := catalog.CloneInheritance(tbl.Inheritance)
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
				if !record.Detached {
					record.Parent = domain.Ref(next.Epoch(), record.Parent.Address)
				}
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
			for _, c := range tbl.Constraints.NotNull {
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
			for _, c := range tbl.Constraints.Check {
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
		copy := *tbl
		copy.Inheritance = graph
		next.SetDescriptor(address, &copy)
	}
	return nil
}
