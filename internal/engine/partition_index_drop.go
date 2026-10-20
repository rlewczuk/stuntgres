package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (g *indexMaintenanceGrant) collectPartitionIndexes(auth AuthorizationView, stmt parser.Statement) error {
	add := func(index *catalog.Relation) {
		ref := auth.Ref(index.Address())
		if !slices.Contains(g.indexes, ref) {
			g.indexes = append(g.indexes, ref)
		}
		g.descriptors[index.Address()] = index
		address := index.SQLIndex.Parent
		parentRef := ref.Domain().Ref(ref.Epoch, address)
		if !slices.Contains(g.parents, parentRef) {
			g.parents = append(g.parents, parentRef)
		}
		if table := auth.Catalog.MustRelation(address); table.Partition != nil || index.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			g.partition = true
		}
		g.descriptors[address] = auth.Catalog.MustRelation(address)
	}
	if _, drop := stmt.(*parser.DropIndex); drop {
		selected := false
		for _, target := range g.targets {
			index := g.descriptors[target.Address]
			table := auth.Catalog.MustRelation(index.SQLIndex.Parent)
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
	indexes := make([]*catalog.Relation, 0, len(g.indexes))
	for _, descriptor := range auth.Catalog.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int { return cmp.Compare(a.Address().ObjectID, b.Address().ObjectID) })
	roots := make(map[ids.ObjectAddress]bool)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, target := range g.targets {
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
					for !slices.ContainsFunc(g.targets, func(target ids.ObjectRef) bool { return target.Address == root.Address() }) {
						root = auth.Catalog.MustRelation(root.SQLIndex.PartitionParent)
					}
					table := auth.Catalog.Get(index.SQLIndex.Parent)
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because other objects depend on it", root.Name()), Detail: fmt.Sprintf("constraint %s on table %s depends on index %s", index.SQLIndex.Constraint.Name, table.Name(), root.Name()), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				}
			}
			if !grew {
				break
			}
		}
	} else if v, ok := stmt.(*parser.AlterIndex); ok && v.Action == "attach" && len(g.targets) > 0 {
		// Capture the parent tree's validity frontier and each sibling descriptor
		// before taking any lock. No names are resolved after a wait.
		root := g.descriptors[g.targets[0].Address]
		for root.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
			root = auth.Catalog.MustRelation(root.SQLIndex.PartitionParent)
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
		if g.attach != (ids.ObjectRef{}) {
			roots[g.attach.Address] = true
		}
	} else {
		return nil
	}
	for address := range roots {
		add(auth.Catalog.MustRelation(address))
	}
	return nil
}

func (e *Engine) stagePartitionIndexDrop(stage *CatalogStage, index *catalog.Relation) error {
	if index.SQLIndex.Constraint != nil {
		view := e.stageRead(stage)
		table := view.MustRelation(index.SQLIndex.Parent)
		next := *table
		next.Constraints.Unique = slices.Clone(table.Constraints.Unique)
		next.ObjectRevision++
		next.Constraints.Unique = slices.DeleteFunc(next.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Index == index.Address() })
		next.Constraints.Exclusion = slices.DeleteFunc(slices.Clone(table.Constraints.Exclusion), func(c catalog.ExclusionConstraint) bool { return c.Index == index.Address() })
		if err := stagePartitionDescriptor(stage, &next); err != nil {
			return err
		}
	}
	return stage.Remove(e.catalog.Domain.Ref(stage.Base.Epoch(), index.Address()))
}

func (e *Engine) stagePartitionConstraintDrop(stage *CatalogStage, oid ids.OID) error {
	view := e.stageRead(stage)
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range table.Constraints.Unique {
			if c.Parent != oid {
				continue
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			index := view.MustRelation(c.Index)
			if err := e.stagePartitionIndexDrop(stage, index); err != nil {
				return err
			}
		}
		for _, c := range table.Constraints.Exclusion {
			if c.Parent != oid {
				continue
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			if err := e.stagePartitionIndexDrop(stage, view.MustRelation(c.Index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func partitionIndexDropDependency(view catalog.Read, index *catalog.Relation) error {
	if index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
		return nil
	}
	parent, exists := view.Descriptor(index.SQLIndex.PartitionParent)
	if !exists {
		return ids.ErrCatalogReplaced
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because index %s requires it", index.Name(), parent.Name()), Hint: "You can drop index " + parent.Name() + " instead."}
}

func partitionConstraintDropActions(table *catalog.Relation, actions []parser.AlterTableAction) bool {
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

// The caller owns Engine.mu; all exits release it before candidate validation.
func (e *Engine) executePartitionConstraintDropLocked(scope *StatementScope, auth AuthorizationView, table *catalog.Relation, syntax *parser.AlterTable) (Result, error) {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.indexOutput = scope.output
	for _, observed := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, observed)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, action := range syntax.Actions {
		view := e.stageRead(stage)
		current := view.MustRelation(table.Address())
		found := false
		for _, c := range current.Constraints.Unique {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, table.Name()))
			}
			index := view.MustRelation(c.Index)
			if err := partitionIndexDropDependency(view, index); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionIndexDrop(stage, index); err != nil {
				return Result{}, err
			}
		}
		for _, c := range current.Constraints.Exclusion {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, table.Name()))
			}
			index := view.MustRelation(c.Index)
			if err := partitionIndexDropDependency(view, index); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionIndexDrop(stage, index); err != nil {
				return Result{}, err
			}
		}
		if !found {
			if _, err := notNullCandidate(scope.op, view, current, action, notNullDirect); err != nil {
				return Result{}, err
			}
		}
		roots := make([]ids.ObjectAddress, 0, stage.RemovalCount())
		for address := range stage.Removals() {
			roots = append(roots, address)
		}
		slices.SortFunc(roots, func(a, b ids.ObjectAddress) int {
			if a.ObjectID < b.ObjectID {
				return -1
			}
			if a.ObjectID > b.ObjectID {
				return 1
			}
			return 0
		})
		if err := scope.stageExpressionDependents(stage, roots, action.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	_, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	err = e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
	return Result{}, err
}
