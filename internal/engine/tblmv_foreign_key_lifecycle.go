package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Column retirement already owns local AUTO definitions and shape projection.
// Consume only FK metadata edges from shared discovery; never reenter the
// whole-provider projection controller before the column's own pruning.
func foreignKeyColumnDependencies(provider dropDependencyReader, target ids.ObjectAddress, mode parser.DropMode) ([]domainDependency, error) {
	present := false
	for address, descriptor := range provider.InputDescriptors() {
		if replacement := provider.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && len(d.Constraints.ForeignKeys) != 0 {
			present = true
			break
		}
	}
	if !present {
		return nil, nil
	}
	discovered, err := provider.dependencyClosure([]ids.ObjectAddress{target})
	if err != nil {
		return nil, err
	}
	foreign := discovered[:0]
	for _, edge := range discovered {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		for _, f := range edge.relation.Constraints.ForeignKeys {
			if f.OID == edge.address.ObjectID {
				// The column's existing shape pass owns local AUTO removal,
				// even if discovery first reached this FK through its index.
				if edge.relation.Address().ClassID == target.ClassID && edge.relation.Address().ObjectID == target.ObjectID && slices.Contains(f.Attributes, target.SubID) {
					break
				}
				edge.target = target
				foreign = append(foreign, edge)
				break
			}
		}
	}
	if provider.statementBound() && mode == parser.DropCascade {
		var owners []ids.ObjectRef
		for _, edge := range foreign {
			if edge.automatic {
				continue
			}
			ref := provider.baseRef(edge.relation.Address())
			if !slices.Contains(owners, ref) {
				owners = append(owners, ref)
			}
		}
		slices.SortFunc(owners, func(a, b ids.ObjectRef) int {
			if a.Address.ObjectID < b.Address.ObjectID {
				return -1
			}
			if a.Address.ObjectID > b.Address.ObjectID {
				return 1
			}
			return 0
		})
		for _, ref := range owners {
			if err := provider.acquire(ref, true); err != nil {
				return nil, err
			}
		}
	}
	return foreign, nil
}

// The ordinary dependency closure owns discovery/order/RESTRICT diagnostics.
// This pass consumes only FK definition edges, never the referencing table/data.
func foreignKeyDependents(provider dropDependencyReader, edges []domainDependency) error {
	for _, edge := range edges {
		if edge.address.ClassID != 2606 || edge.relation == nil {
			continue
		}
		if provider.Removed(edge.relation.Address()) {
			continue
		}
		old := edge.relation
		if replacement, ok := provider.Replacement(old.Address()).(*catalog.Relation); ok {
			old = replacement
		}
		for i, f := range old.Constraints.ForeignKeys {
			if f.OID != edge.address.ObjectID {
				continue
			}
			next := *old
			next.Constraints.ForeignKeys = make([]catalog.ForeignKeyConstraint, 0, len(old.Constraints.ForeignKeys)-1)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[:i]...)
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[i+1:]...)
			var err error
			next.ObjectRevision, err = provider.revision(old.Revision())
			if err != nil {
				return err
			}
			if err := provider.Replace(&next); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
