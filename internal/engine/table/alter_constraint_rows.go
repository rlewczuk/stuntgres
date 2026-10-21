package table

import (
	"stuntgres/internal/engine/catalog"
)

func ValidCheckTraversal(parent *catalog.Relation) *catalog.Relation {
	valid := 0
	for _, c := range parent.Constraints.Check {
		if !c.NotValid {
			valid++
		}
	}
	if valid == len(parent.Constraints.Check) {
		return parent
	}
	next := *parent
	next.Constraints.Check = make([]catalog.CheckConstraint, 0, valid)
	for _, c := range parent.Constraints.Check {
		if !c.NotValid {
			next.Constraints.Check = append(next.Constraints.Check, c)
		}
	}
	return &next
}
