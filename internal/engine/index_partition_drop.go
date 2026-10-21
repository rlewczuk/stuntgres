package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// stagePartitionIndexDrop and stagePartitionConstraintDrop serve ALTER
// TABLE and the partition constraint tests; neither needs Engine.mu.
func (e *Engine) stagePartitionIndexDrop(stage *CatalogStage, idx *catalog.Relation) error {
	return index.StagePartitionIndexDrop(stage, idx)
}

func (e *Engine) stagePartitionConstraintDrop(stage *CatalogStage, oid ids.OID) error {
	return index.StagePartitionConstraintDrop(stage, oid)
}

// The caller owns Engine.mu; all exits release it before candidate validation.
func (e *Engine) executePartitionConstraintDropLocked(scope *StatementScope, auth AuthorizationView, tbl *catalog.Relation, syntax *parser.AlterTable) (Result, error) {
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
		current := view.MustRelation(tbl.Address())
		found := false
		for _, c := range current.Constraints.Unique {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, tbl.Name()))
			}
			idx := view.MustRelation(c.Index)
			if err := index.PartitionIndexDropDependency(view, idx); err != nil {
				return Result{}, err
			}
			if err := index.StagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := index.StagePartitionIndexDrop(stage, idx); err != nil {
				return Result{}, err
			}
		}
		for _, c := range current.Constraints.Exclusion {
			if c.Name != action.Constraint.Text {
				continue
			}
			found = true
			if c.Parent != 0 {
				return Result{}, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, tbl.Name()))
			}
			idx := view.MustRelation(c.Index)
			if err := index.PartitionIndexDropDependency(view, idx); err != nil {
				return Result{}, err
			}
			if err := index.StagePartitionConstraintDrop(stage, c.OID); err != nil {
				return Result{}, err
			}
			if err := index.StagePartitionIndexDrop(stage, idx); err != nil {
				return Result{}, err
			}
		}
		if !found {
			if _, err := table.NotNullCandidate(scope.op, view, current, action, table.NotNullDirect); err != nil {
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
