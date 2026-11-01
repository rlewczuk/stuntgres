package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
)

// construction is physical history, not an executable recipe or a publication
// receipt. Only a full builder starts clean history. Transport records its exact
// logical mapping and keeps the previous history, including outstanding debt.
type construction struct {
	recipe         *catalog.IndexDefinition
	previous       *construction
	source, target *catalog.Relation
}

func newConstruction(recipe *catalog.IndexDefinition) *construction {
	return &construction{recipe: recipe}
}

func transportConstruction(root *storage.IndexRoot, source, target *catalog.Relation) *construction {
	previous, _ := root.Construction.(*construction)
	return &construction{previous: previous, source: source, target: target}
}

// ConstructionMatches is a structural maintenance decision only. Stale physical
// providers are never validated, authorized or executed through this history.
func ConstructionMatches(root *storage.IndexRoot, current *catalog.Relation) bool {
	if root == nil || current == nil || current.SQLIndex == nil {
		return false
	}
	history, _ := root.Construction.(*construction)
	for history != nil {
		if history.recipe != nil {
			physical := *current
			physical.SQLIndex = history.recipe
			return SameIndexKeySemantics(&physical, current)
		}
		if history.source == nil || history.target == nil || !SameIndexKeySemantics(history.target, current) {
			return false
		}
		current, history = history.source, history.previous
	}
	return false
}
