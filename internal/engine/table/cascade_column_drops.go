package table

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CascadeColumnDrops consumes captured effects only: one metadata cook and
// physical projection per surviving relation, then domain CHECK/provider
// retirement. It never initiates dependency discovery.
func CascadeColumnDrops(r DropDependencyReader, relations []*catalog.Relation, edges []DomainDependency, targets []ids.ObjectAddress, project func(old, next *catalog.Relation) error) error {
	var err error
	for _, captured := range relations {
		if r.Removed(captured.Address()) {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, captured.Address()) {
			continue
		}
		// Captured identity/revision and physical predecessor remain authority.
		// Earlier CHECK/default/policy effects can replace metadata pointers;
		// cook their current maps without changing the projector's predecessor
		// or allocating a second revision for those semantic effects.
		old := r.Descriptor(captured.Address()).(*catalog.Relation)
		var attributes []int32
		for _, edge := range edges {
			if edge.Column == "" || edge.Relation.Address() != old.Address() {
				continue
			}
			// Prior real effects may have consumed this stable attribute.
			if old.IndexForAttribute(edge.Address.SubID) < 0 {
				continue
			}
			attributes = append(attributes, edge.Address.SubID)
		}
		if len(attributes) == 0 {
			continue
		}
		if err := r.InheritanceAlterGuard(old); err != nil {
			return err
		}
		next, err := dropColumnsMetadata(r.Context(), r.BaseRead(), r.Descriptor, old, attributes)
		if err != nil {
			return err
		}
		revision, err := r.Revision(captured.Revision())
		if err != nil {
			return err
		}
		next.ObjectRevision = revision
		if err := r.Replace(next); err != nil {
			return err
		}
		if err := project(captured, next); err != nil {
			return err
		}
		// The final owner shape no longer needs local defaults or index
		// metadata. Descendants were already preflighted by this same closure.
		columns := make([]ids.ObjectAddress, len(attributes))
		for i, attribute := range attributes {
			columns[i] = old.Address()
			columns[i].SubID = attribute
		}
		if err := RemoveColumnSequences(r, columns); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if edge.Domain == nil {
			continue
		}
		if r.Removed(edge.Domain.Address()) {
			continue
		}
		if err := RemoveDomainCheck(r, edge.Domain, edge.Address.ObjectID); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if RetiredProviderClass(target.ClassID) {
			if err = r.Remove(r.BaseRef(target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	return nil
}
