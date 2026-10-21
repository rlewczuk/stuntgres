package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
)

func BindingRuleRelation(b analyze.Binding) *catalog.Relation {
	return PhysicalBinding(b).Relation
}
