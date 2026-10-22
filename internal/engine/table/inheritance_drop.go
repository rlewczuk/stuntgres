package table

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// InheritanceDropClosure is every ordinary table of view in the inheritance
// trees below roots, roots included, ordered by OID: the tables a DROP of an
// inheritance participant locks. Column, missing and non-table roots
// contribute nothing.
func InheritanceDropClosure(view catalog.Read, roots []ids.ObjectAddress) []ids.ObjectAddress {
	var tables []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool)
	var visit func(ids.ObjectAddress)
	visit = func(address ids.ObjectAddress) {
		if seen[address] {
			return
		}
		seen[address] = true
		table, ok := view.Relation(address)
		if !ok || table.RelationKind != catalog.TableRelation || address.SubID != 0 {
			return
		}
		tables = append(tables, address)
		for _, child := range catalog.InheritanceChildren(view, address) {
			visit(child.Address())
		}
	}
	for _, root := range roots {
		visit(root)
	}
	slices.SortFunc(tables, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	return tables
}

// ColumnDropMultiple: a recursive DROP can remove an origin without deleting the child's column.
// The root is one physical deletion; a second exists exactly when an immediate
// child loses its sole nonlocal origin. Without that first deletion no deeper
// column can disappear, so this threshold needs neither a simulated graph nor
// a count of descendants which merely retain/localize their columns.
//
// ColumnDropMultiple reports whether dropping the column target of base
// through this plan deletes more than one physical column.
func (p *InheritanceAlterPlan) ColumnDropMultiple(base catalog.Read, target ids.ObjectAddress) bool {
	if p.Only {
		return false
	}
	address := target
	address.SubID = 0
	source := base.MustRelation(address)
	slot := source.IndexForAttribute(target.SubID)
	if slot < 0 {
		return false
	}
	name := source.Columns[slot].Name
	rootSlot, exists := p.Root.Index[name]
	if !exists {
		return false
	}
	attribute := p.Root.Attributes[rootSlot]
	for _, child := range p.Targets {
		if child.Inheritance == nil {
			continue
		}
		childSlot, exists := child.Index[name]
		if !exists {
			continue
		}
		fact := child.Inheritance.Columns[child.Attributes[childSlot]]
		if !fact.Local && len(fact.Origins) == 1 && fact.Origins[0].Parent == p.Root.Address() && fact.Origins[0].Attribute == attribute {
			return true
		}
	}
	return false
}
