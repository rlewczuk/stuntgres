package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

func (e *Engine) stagePartitionIndexDrop(stage *CatalogStage, idx *catalog.Relation) error {
	if idx.SQLIndex.Constraint != nil {
		view := e.stageRead(stage)
		table := view.MustRelation(idx.SQLIndex.Parent)
		next := *table
		next.Constraints.Unique = slices.Clone(table.Constraints.Unique)
		next.ObjectRevision++
		next.Constraints.Unique = slices.DeleteFunc(next.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Index == idx.Address() })
		next.Constraints.Exclusion = slices.DeleteFunc(slices.Clone(table.Constraints.Exclusion), func(c catalog.ExclusionConstraint) bool { return c.Index == idx.Address() })
		if err := index.StagePartitionDescriptor(stage, &next); err != nil {
			return err
		}
	}
	return stage.Remove(e.catalog.Domain.Ref(stage.Base.Epoch(), idx.Address()))
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

// The caller owns Engine.mu; all exits release it before candidate validation.
func (e *Engine) executePartitionConstraintDropLocked(scope *StatementScope, auth AuthorizationView, table *catalog.Relation, syntax *parser.AlterTable) (Result, error) {
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.indexes().Output = scope.output
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
			idx := view.MustRelation(c.Index)
			if err := index.PartitionIndexDropDependency(view, idx); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionIndexDrop(stage, idx); err != nil {
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
			idx := view.MustRelation(c.Index)
			if err := index.PartitionIndexDropDependency(view, idx); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := e.stagePartitionIndexDrop(stage, idx); err != nil {
				return Result{}, err
			}
		}
		if !found {
			if _, err := NotNullCandidate(scope.op, view, current, action, NotNullDirect); err != nil {
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
	err = e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
	return Result{}, err
}
