package catalog

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func InheritanceHasChildren(v Read, parent ids.ObjectAddress) bool {
	for _, descriptor := range v.Descriptors() {
		if d, ok := descriptor.(*Relation); ok && d.Inheritance.HasParent(parent) {
			return true
		}
	}
	return false
}

func InheritanceChildren(v Read, parent ids.ObjectAddress) []*Relation {
	var children []*Relation
	for address := range v.Descriptors() {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		d, ok := v.Relation(address)
		if ok && d.Inheritance.HasParent(parent) {
			children = append(children, d)
		}
	}
	slices.SortFunc(children, func(a, b *Relation) int {
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

func InheritanceParticipant(v Read, d *Relation) bool {
	return d.Inheritance != nil || InheritanceHasChildren(v, d.Address())
}

func CloneInheritance(graph *TableInheritance) *TableInheritance {
	if graph == nil {
		return nil
	}
	next := *graph
	next.Parents = slices.Clone(graph.Parents)
	next.Columns = maps.Clone(graph.Columns)
	next.Checks = maps.Clone(graph.Checks)
	next.NotNull = maps.Clone(graph.NotNull)
	return &next
}

// Graph validation establishes name/origin identity; live row layout still has
// to be translated, because earlier parents may precede this named parent.
func InheritanceQueryLayout(parent, child *Relation) ([]int, error) {
	identity := len(parent.Columns) == len(child.Columns)
	for i, column := range parent.Columns {
		slot, ok := child.Index[column.Name]
		if !ok || !SamePartitionColumnUsage(child.Columns[slot].Usage, column.Usage) {
			return nil, ids.ErrCatalogReplaced
		}
		identity = identity && slot == i
	}
	if identity {
		return nil, nil
	}
	layout := make([]int, len(parent.Columns))
	for i, column := range parent.Columns {
		layout[i] = child.Index[column.Name]
	}
	return layout, nil
}

func HierarchyRelation(d *Relation) bool { return d.Partition != nil || d.Membership != nil }

func SamePartitionColumnUsage(a, b sqltype.Usage) bool {
	return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation.ID == b.Collation.ID
}
