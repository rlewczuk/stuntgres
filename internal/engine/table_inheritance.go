package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func inheritanceHasChildren(v catalog.Read, parent ids.ObjectAddress) bool {
	for _, descriptor := range v.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			return true
		}
	}
	return false
}

func inheritanceChildren(v catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	var children []*catalog.Relation
	for address := range v.Descriptors() {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		d, ok := v.Relation(address)
		if ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			children = append(children, d)
		}
	}
	slices.SortFunc(children, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return children
}
func inheritanceParticipant(v catalog.Read, d *catalog.Relation) bool {
	return d.Inheritance != nil || inheritanceHasChildren(v, d.Address())
}
