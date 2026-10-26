package table

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// PruneCompositeFieldChecks consumes CHECK identities from the fully preflighted
// canonical column closure. It owns inherited CHECK origin pruning and the
// admitted typed-table exemption, not discovery, diagnostics or notices. Returned
// identities are already consumed; canonical edges still own their notices.
func PruneCompositeFieldChecks(provider DropDependencyReader, edges []DomainDependency) ([]ids.ObjectAddress, error) {
	var consumed []ids.ObjectAddress
	for _, edge := range edges {
		if edge.Address.ClassID != 2606 {
			continue
		}
		if edge.Domain != nil {
			if provider.Removed(edge.Domain.Address()) {
				continue
			}
			if err := RemoveDomainCheck(provider, edge.Domain, edge.Address.ObjectID); err != nil {
				return nil, err
			}
		} else {
			if edge.Relation == nil || provider.Removed(edge.Relation.Address()) {
				continue
			}
			relation := provider.Descriptor(edge.Relation.Address()).(*catalog.Relation)
			index := slices.IndexFunc(relation.Constraints.Check, func(check catalog.CheckConstraint) bool { return check.OID == edge.Address.ObjectID })
			if index < 0 {
				continue
			}
			if !provider.TypedFieldCheckOwner(relation) {
				if err := provider.InheritanceAlterGuard(relation); err != nil {
					return nil, err
				}
			}
			next := *relation
			var err error
			next.ObjectRevision, err = provider.Revision(relation.Revision())
			if err != nil {
				return nil, err
			}
			next.Constraints.Check = slices.Delete(slices.Clone(relation.Constraints.Check), index, index+1)
			if relation.Inheritance != nil {
				graph := *relation.Inheritance
				graph.Checks = maps.Clone(graph.Checks)
				delete(graph.Checks, relation.Constraints.Check[index].Name)
				next.Inheritance = &graph
			}
			if err := provider.Replace(&next); err != nil {
				return nil, err
			}
		}
		consumed = append(consumed, edge.Address)
	}
	return consumed, nil
}

// RemoveDomainCheck replaces domain d, as the drop already changed it, with a
// copy without its CHECK constraint oid and a new revision.
func RemoveDomainCheck(provider DropDependencyReader, d *catalog.Type, oid ids.OID) error {
	if replacement, ok := provider.Replacement(d.Address()).(*catalog.Type); ok {
		d = replacement
	}
	next, err := typedef.WithoutDomainCheck(d, oid)
	if err != nil {
		return err
	}
	next.ObjectRevision, err = provider.Revision(d.ObjectRevision)
	if err != nil {
		return err
	}
	return provider.Replace(next)
}
