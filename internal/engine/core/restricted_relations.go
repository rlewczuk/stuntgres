package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// AdmitRelation is metadata-only and uses the operation's settings, not the
// current view owner's identity. Ordinary relations require no handler lookup.
func (v AuthorizationView) AdmitRelation(relation *catalog.Relation) error {
	if relation.RelationKind != catalog.ViewRelation && relation.RelationKind != catalog.ForeignRelation && !(relation.RelationKind == catalog.CatalogRelation && relation.CatalogView) {
		return nil
	}
	return admitRelation(v.Catalog, relation, v.RestrictedRelations())
}

func admitRelation(read catalog.Read, relation *catalog.Relation, policy settings.RestrictedRelationKinds) error {
	if err := stored.AdmitView(relation, policy); err != nil {
		return err
	}
	if relation.RelationKind == catalog.ForeignRelation && policy&settings.RestrictForeignTable != 0 {
		_, err := fdw.RelationHandler(read, relation, policy)
		return err
	}
	return nil
}

// Run only at execution: foreign Describe/Prepare remains metadata-only. The
// existing source closure admits nested definitions at its own authority fence.
func admitBindingRelations(auth AuthorizationView, b analyze.Binding) error {
	policy := auth.RestrictedRelations()
	if policy == 0 {
		return nil
	}
	if target := view.PhysicalBinding(b).Relation; target != nil {
		if err := admitRelation(auth.Catalog, target, policy); err != nil {
			return err
		}
	}
	for _, leaf := range b.QuerySources {
		if !leaf.AnalysisOnly {
			if err := admitRelation(auth.Catalog, leaf.Relation, policy); err != nil {
				return err
			}
		}
	}
	return nil
}
