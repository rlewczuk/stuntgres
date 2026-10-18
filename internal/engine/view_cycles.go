package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
)

// Publication permits cyclic rewrite definitions. Expansion rejects only an
// active-path recurrence, not repeated occurrences of a shared acyclic source.
// The same current identity-bound graph supplies inherited visibility demand;
// stored query markers describe only expressions local to that definition.
func validateViewReadGraph(catalog CatalogRead, leaves []queryLeaf) (bool, error) {
	hasView := false
	for _, leaf := range leaves {
		if leaf.relation.RelationKind == viewRelation {
			hasView = true
			break
		}
	}
	if !hasView {
		return false, nil
	}
	active, done := make(map[ids.ObjectAddress]bool), make(map[ids.ObjectAddress]bool)
	visibilityDemand := false
	var visit func(*relationDescriptor) error
	visit = func(d *relationDescriptor) error {
		if d.RelationKind != viewRelation || done[d.address] {
			return nil
		}
		if active[d.address] {
			return sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.name))
		}
		active[d.address] = true
		visibilityDemand = visibilityDemand || viewOf(d).query.visibilitySnapshot
		for _, leaf := range viewOf(d).sources {
			child, ok := catalog.Relation(leaf.ref.Address)
			if !ok || !storedViewReferenceValid(catalog, d, leaf.ref) {
				return ids.ErrCatalogReplaced
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		delete(active, d.address)
		done[d.address] = true
		return nil
	}
	for _, leaf := range leaves {
		if leaf.relation.RelationKind != viewRelation {
			continue
		}
		current, ok := catalog.Relation(leaf.relation.address)
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
		if err := visit(current); err != nil {
			return false, err
		}
	}
	return visibilityDemand, nil
}
