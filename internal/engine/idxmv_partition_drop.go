package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (g *indexMaintenanceGrant) CollectPartitionIndexes(view access.View, stmt parser.Statement) error {
	add := func(index *catalog.Relation) {
		ref := view.Ref(index.Address())
		if !slices.Contains(g.Indexes, ref) {
			g.Indexes = append(g.Indexes, ref)
		}
		g.Descriptors[index.Address()] = index
		address := index.SQLIndex.Parent
		parentRef := ref.Domain().Ref(ref.Epoch, address)
		if !slices.Contains(g.Parents, parentRef) {
			g.Parents = append(g.Parents, parentRef)
		}
		if table := view.Catalog.MustRelation(address); table.Partition != nil || index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			g.Partition = true
		}
		g.Descriptors[address] = view.Catalog.MustRelation(address)
	}
	if _, drop := stmt.(*parser.DropIndex); drop {
		selected := false
		for _, target := range g.Targets {
			index := g.Descriptors[target.Address]
			table := view.Catalog.MustRelation(index.SQLIndex.Parent)
			if table.Partition != nil || index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
				selected = true
				break
			}
		}
		if !selected {
			return nil
		}
	} else if alter, ok := stmt.(*parser.AlterIndex); !ok || alter.Action != "attach" {
		return nil
	}
	indexes := make([]*catalog.Relation, 0, len(g.Indexes))
	for _, descriptor := range view.Catalog.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int { return cmp.Compare(a.Address().ObjectID, b.Address().ObjectID) })
	roots := make(map[ids.ObjectAddress]bool)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, target := range g.Targets {
			roots[target.Address] = true
		}
		for {
			grew := false
			for _, index := range indexes {
				if !roots[index.SQLIndex.PartitionParent] || roots[index.Address()] {
					continue
				}
				roots[index.Address()] = true
				grew = true
				if index.SQLIndex.Constraint != nil && drop.Mode != parser.DropCascade {
					root := index
					for !slices.ContainsFunc(g.Targets, func(target ids.ObjectRef) bool { return target.Address == root.Address() }) {
						root = view.Catalog.MustRelation(root.SQLIndex.PartitionParent)
					}
					table := view.Catalog.Get(index.SQLIndex.Parent)
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because other objects depend on it", root.Name()), Detail: fmt.Sprintf("constraint %s on table %s depends on index %s", index.SQLIndex.Constraint.Name, table.Name(), root.Name()), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				}
			}
			if !grew {
				break
			}
		}
	} else if v, ok := stmt.(*parser.AlterIndex); ok && v.Action == "attach" && len(g.Targets) > 0 {
		// Capture the parent tree's validity frontier and each sibling descriptor
		// before taking any lock. No names are resolved after a wait.
		root := g.Descriptors[g.Targets[0].Address]
		for root.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			root = view.Catalog.MustRelation(root.SQLIndex.PartitionParent)
		}
		roots[root.Address()] = true
		for {
			grew := false
			for _, index := range indexes {
				if roots[index.SQLIndex.PartitionParent] && !roots[index.Address()] {
					roots[index.Address()] = true
					grew = true
				}
			}
			if !grew {
				break
			}
		}
		if g.Attach != (ids.ObjectRef{}) {
			roots[g.Attach.Address] = true
		}
	} else {
		return nil
	}
	for address := range roots {
		add(view.Catalog.MustRelation(address))
	}
	return nil
}

func PartitionIndexDropDependency(view catalog.Read, index *catalog.Relation) error {
	if index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return nil
	}
	parent, exists := view.Descriptor(index.SQLIndex.PartitionParent)
	if !exists {
		return ids.ErrCatalogReplaced
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because index %s requires it", index.Name(), parent.Name()), Hint: "You can drop index " + parent.Name() + " instead."}
}

func PartitionConstraintDropActions(table *catalog.Relation, actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	selected := false
	for _, action := range actions {
		if action.Action != parser.DropConstraint {
			return false
		}
		found := false
		for _, c := range table.Constraints.Unique {
			if c.Name == action.Constraint.Text {
				found = true
				break
			}
		}
		found = found || slices.ContainsFunc(table.Constraints.Exclusion, func(c catalog.ExclusionConstraint) bool { return c.Name == action.Constraint.Text })
		if found {
			selected = true
			continue
		}
		if slices.ContainsFunc(table.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Name == action.Constraint.Text }) ||
			slices.ContainsFunc(table.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == action.Constraint.Text }) ||
			slices.ContainsFunc(table.Constraints.ForeignKeys, func(c catalog.ForeignKeyConstraint) bool { return c.Name == action.Constraint.Text }) {
			return false
		}
	}
	return selected
}
