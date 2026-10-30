package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// Publication permits cyclic rewrite definitions. Expansion rejects only an
// active-path recurrence, not repeated occurrences of a shared acyclic source.
// The same current identity-bound graph supplies inherited visibility demand;
// stored query markers describe only expressions local to that definition.
func ValidateViewReadGraph(view catalog.Read, leaves []exec.Leaf, restricted settings.RestrictedRelationKinds) (bool, error) {
	hasView := false
	for _, leaf := range leaves {
		if leaf.AnalysisOnly {
			continue
		}
		if leaf.Relation.RelationKind == catalog.ViewRelation {
			hasView = true
		} else if leaf.Relation.RelationKind == catalog.CatalogRelation && leaf.Relation.CatalogView && restricted&settings.RestrictView != 0 {
			current, ok := view.Relation(leaf.Relation.Address())
			if !ok {
				return false, ids.ErrCatalogReplaced
			}
			if err := AdmitView(current, restricted); err != nil {
				return false, err
			}
		}
	}
	if !hasView {
		return false, nil
	}
	active, done := make(map[ids.ObjectAddress]bool), make(map[ids.ObjectAddress]bool)
	visibilityDemand := false
	var visit func(*catalog.Relation) error
	visit = func(d *catalog.Relation) error {
		if done[d.Address()] {
			return nil
		}
		if err := AdmitView(d, restricted); err != nil {
			return err
		}
		if d.RelationKind != catalog.ViewRelation {
			return nil
		}
		if active[d.Address()] {
			return sqlError("42P17", fmt.Sprintf(`infinite recursion detected in rules for relation "%s"`, d.Name()))
		}
		active[d.Address()] = true
		visibilityDemand = visibilityDemand || ViewOf(d).query.VisibilitySnapshot
		for _, leaf := range ViewOf(d).sources {
			if leaf.AnalysisOnly {
				continue
			}
			child, ok := view.Relation(leaf.Ref.Address)
			if !ok || !ViewReferenceValid(view, d, leaf.Ref) {
				return ids.ErrCatalogReplaced
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		delete(active, d.Address())
		done[d.Address()] = true
		return nil
	}
	for _, leaf := range leaves {
		if leaf.AnalysisOnly || leaf.Relation.RelationKind != catalog.ViewRelation {
			continue
		}
		current, ok := view.Relation(leaf.Relation.Address())
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
		if err := visit(current); err != nil {
			return false, err
		}
	}
	return visibilityDemand, nil
}

// AdmitView uses object identity, never namespace spelling or ownership.
func AdmitView(d *catalog.Relation, restricted settings.RestrictedRelationKinds) error {
	if restricted&settings.RestrictView != 0 && (d.RelationKind == catalog.ViewRelation || d.RelationKind == catalog.CatalogRelation && d.CatalogView) && d.Address().ObjectID >= 16384 {
		return sqlError("55000", fmt.Sprintf(`access to non-system view "%s" is restricted`, d.Name()))
	}
	return nil
}
