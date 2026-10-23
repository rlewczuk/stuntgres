package partition

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// StageConstraintDrop stages one DROP CONSTRAINT action of an ALTER TABLE on
// relation, a partition hierarchy member: each UNIQUE or EXCLUDE constraint
// of that name with its partitioned index (42P16 when it is inherited), or,
// when none matches, the NOT NULL check of table.NotNullCandidate. It returns
// every removal staged in removals so far, sorted by object ID, as the roots
// of the caller's expression-dependents pass.
//
// removals is the candidate that stage stages into. The caller holds nothing;
// stage.RemoveRelation takes Engine.mu.
func StageConstraintDrop(op *session.Operation, stage index.DropStage, removals *catalog.Candidate, relation *catalog.Relation, action parser.AlterTableAction) ([]ids.ObjectAddress, error) {
	view := stage.StageRead()
	current := view.MustRelation(relation.Address())
	found := false
	for _, c := range current.Constraints.Unique {
		if c.Name != action.Constraint.Text {
			continue
		}
		found = true
		if c.Parent != 0 {
			return nil, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, relation.Name()))
		}
		idx := view.MustRelation(c.Index)
		if err := index.PartitionIndexDropDependency(view, idx); err != nil {
			return nil, err
		}
		if err := index.StagePartitionConstraintDrop(stage, c.OID); err != nil {
			return nil, err
		}
		if err := index.StagePartitionIndexDrop(stage, idx); err != nil {
			return nil, err
		}
	}
	for _, c := range current.Constraints.Exclusion {
		if c.Name != action.Constraint.Text {
			continue
		}
		found = true
		if c.Parent != 0 {
			return nil, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, relation.Name()))
		}
		idx := view.MustRelation(c.Index)
		if err := index.PartitionIndexDropDependency(view, idx); err != nil {
			return nil, err
		}
		if err := index.StagePartitionConstraintDrop(stage, c.OID); err != nil {
			return nil, err
		}
		if err := index.StagePartitionIndexDrop(stage, idx); err != nil {
			return nil, err
		}
	}
	if !found {
		if _, err := table.NotNullCandidate(op, view, current, action, table.NotNullDirect); err != nil {
			return nil, err
		}
	}
	roots := make([]ids.ObjectAddress, 0, removals.RemovalCount())
	for address := range removals.Removals() {
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
	return roots, nil
}
