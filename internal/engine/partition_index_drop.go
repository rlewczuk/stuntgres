package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (g *indexMaintenanceGrant) collectPartitionIndexes(auth AuthorizationView, stmt parser.Statement) error {
	add := func(index *relationDescriptor) {
		ref := auth.engine.catalog.domain.Ref(auth.catalog.Epoch(), index.address)
		if !slices.Contains(g.indexes, ref) {
			g.indexes = append(g.indexes, ref)
		}
		g.descriptors[index.address] = index
		address := index.sqlIndex.parent
		parentRef := ref.Domain().Ref(ref.Epoch, address)
		if !slices.Contains(g.parents, parentRef) {
			g.parents = append(g.parents, parentRef)
		}
		if table := auth.catalog.state.descriptors[address].(*relationDescriptor); table.partition != nil || index.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
			g.partition = true
		}
		g.descriptors[address] = auth.catalog.state.descriptors[address].(*relationDescriptor)
	}
	if _, drop := stmt.(*parser.DropIndex); drop {
		selected := false
		for _, target := range g.targets {
			index := g.descriptors[target.Address]
			table := auth.catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			if table.partition != nil || index.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
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
	indexes := make([]*relationDescriptor, 0, len(g.indexes))
	for _, descriptor := range auth.catalog.state.descriptors {
		if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int { return cmp.Compare(a.address.ObjectID, b.address.ObjectID) })
	roots := make(map[ids.ObjectAddress]bool)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, target := range g.targets {
			roots[target.Address] = true
		}
		for {
			grew := false
			for _, index := range indexes {
				if !roots[index.sqlIndex.partitionParent] || roots[index.address] {
					continue
				}
				roots[index.address] = true
				grew = true
				if index.sqlIndex.constraint != nil && drop.Mode != parser.DropCascade {
					root := index
					for !slices.ContainsFunc(g.targets, func(target ids.ObjectRef) bool { return target.Address == root.address }) {
						root = auth.catalog.state.descriptors[root.sqlIndex.partitionParent].(*relationDescriptor)
					}
					table := auth.catalog.state.descriptors[index.sqlIndex.parent]
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because other objects depend on it", root.name), Detail: fmt.Sprintf("constraint %s on table %s depends on index %s", index.sqlIndex.constraint.Name, table.Name(), root.name), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
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
		for root.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
			root = auth.catalog.state.descriptors[root.sqlIndex.partitionParent].(*relationDescriptor)
		}
		roots[root.address] = true
		for {
			grew := false
			for _, index := range indexes {
				if roots[index.sqlIndex.partitionParent] && !roots[index.address] {
					roots[index.address] = true
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
		add(auth.catalog.state.descriptors[address].(*relationDescriptor))
	}
	return nil
}

func (e *Engine) stagePartitionIndexDrop(stage *CatalogStage, index *relationDescriptor) error {
	if index.sqlIndex.constraint != nil {
		view := e.stageRead(stage)
		table := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		next := *table
		next.uniqueConstraints = slices.Clone(table.uniqueConstraints)
		next.revision++
		next.uniqueConstraints = slices.DeleteFunc(next.uniqueConstraints, func(c uniqueConstraint) bool { return c.Index == index.address })
		next.exclusionConstraints = slices.DeleteFunc(slices.Clone(table.exclusionConstraints), func(c exclusionConstraint) bool { return c.Index == index.address })
		if err := stagePartitionDescriptor(stage, &next); err != nil {
			return err
		}
	}
	return stage.Remove(e.catalog.domain.Ref(stage.base.Epoch(), index.address))
}

func (e *Engine) stagePartitionConstraintDrop(stage *CatalogStage, oid ids.OID) error {
	view := e.stageRead(stage)
	for _, descriptor := range view.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, c := range table.uniqueConstraints {
			if c.Parent != oid {
				continue
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			index := view.state.descriptors[c.Index].(*relationDescriptor)
			if err := e.stagePartitionIndexDrop(stage, index); err != nil {
				return err
			}
		}
		for _, c := range table.exclusionConstraints {
			if c.Parent != oid {
				continue
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return err
			}
			if err := e.stagePartitionIndexDrop(stage, view.state.descriptors[c.Index].(*relationDescriptor)); err != nil {
				return err
			}
		}
	}
	return nil
}

func partitionIndexDropDependency(view CatalogRead, index *relationDescriptor) error {
	if index.sqlIndex.partitionParent == (ids.ObjectAddress{}) {
		return nil
	}
	parent, exists := view.Descriptor(index.sqlIndex.partitionParent)
	if !exists {
		return ErrCatalogReplaced
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop index %s because index %s requires it", index.name, parent.Name()), Hint: "You can drop index " + parent.Name() + " instead."}
}

func partitionConstraintDropActions(table *relationDescriptor, actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	selected := false
	for _, action := range actions {
		if action.Action != parser.DropConstraint {
			return false
		}
		found := false
		for _, c := range table.uniqueConstraints {
			if c.Name == action.Constraint.Text {
				found = true
				break
			}
		}
		found = found || slices.ContainsFunc(table.exclusionConstraints, func(c exclusionConstraint) bool { return c.Name == action.Constraint.Text })
		if found {
			selected = true
			continue
		}
		if slices.ContainsFunc(table.notNullConstraints, func(c notNullConstraint) bool { return c.Name == action.Constraint.Text }) ||
			slices.ContainsFunc(table.checkConstraints, func(c checkConstraint) bool { return c.Name == action.Constraint.Text }) ||
			slices.ContainsFunc(table.foreignKeys, func(c foreignKeyConstraint) bool { return c.Name == action.Constraint.Text }) {
			return false
		}
	}
	return selected
}

// The caller owns Engine.mu; all exits release it before candidate validation.
func (e *Engine) executePartitionConstraintDropLocked(scope *StatementScope, auth AuthorizationView, table *relationDescriptor, syntax *parser.AlterTable) (Result, error) {
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, scope.op.session)
	stage.indexOutput = scope.output
	for _, observed := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, observed)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for _, action := range syntax.Actions {
		view := e.stageRead(stage)
		current := view.state.descriptors[table.address].(*relationDescriptor)
		found := false
		for _, c := range current.uniqueConstraints {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, table.name))
			}
			index := view.state.descriptors[c.Index].(*relationDescriptor)
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
		for _, c := range current.exclusionConstraints {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, table.name))
			}
			index := view.state.descriptors[c.Index].(*relationDescriptor)
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
		roots := make([]ids.ObjectAddress, 0, len(stage.removals))
		for address := range stage.removals {
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
