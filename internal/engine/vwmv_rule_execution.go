package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
)

func bindingRuleRelation(b analyze.Binding) *catalog.Relation {
	return physicalBinding(b).Relation
}
