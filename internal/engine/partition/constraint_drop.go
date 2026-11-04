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

// ConstraintDrop captures the original descendant-before-primary physical
// order before the first backing index is removed.
type ConstraintDrop struct {
	children    []*index.ConstraintDropPlan
	primary     []*catalog.Relation
	constraints []ids.ObjectAddress
	Roots       []ids.ObjectAddress
	source      catalog.Read
	consumed    bool
}

func PrepareConstraintDrop(op *session.Operation, stage index.DropStage, removals *catalog.Candidate, relation *catalog.Relation, action parser.AlterTableAction) (*ConstraintDrop, error) {
	view := stage.StageRead()
	current := view.MustRelation(relation.Address())
	plan := &ConstraintDrop{source: view}
	add := func(name string, oid, parent ids.OID, address ids.ObjectAddress) error {
		if parent != 0 {
			return sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, name, relation.Name()))
		}
		idx := view.MustRelation(address)
		if err := index.PartitionIndexDropDependency(view, idx); err != nil {
			return err
		}
		children, err := index.PreparePartitionConstraintDrop(view, view.Descriptors(), view.Get, oid)
		if err != nil {
			return err
		}
		plan.children = append(plan.children, children)
		plan.primary = append(plan.primary, idx)
		plan.constraints = append(plan.constraints, ids.ObjectAddress{ClassID: 2606, ObjectID: oid})
		for _, child := range children.Indexes {
			plan.Roots = append(plan.Roots, child.Address())
		}
		plan.Roots = append(plan.Roots, address)
		return nil
	}
	for _, c := range current.Constraints.Unique {
		if c.Name == action.Constraint.Text {
			if err := add(c.Name, c.OID, c.Parent, c.Index); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range current.Constraints.Exclusion {
		if c.Name == action.Constraint.Text {
			if err := add(c.Name, c.OID, c.Parent, c.Index); err != nil {
				return nil, err
			}
		}
	}
	if len(plan.primary) == 0 {
		if _, err := table.NotNullCandidate(op, view, current, action, table.NotNullDirect); err != nil {
			return nil, err
		}
	}
	for address := range removals.Removals() {
		if !slices.Contains(plan.Roots, address) {
			plan.Roots = append(plan.Roots, address)
		}
	}
	slices.SortFunc(plan.Roots, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	return plan, nil
}

func (plan *ConstraintDrop) Apply(stage index.DropStage) error {
	if plan == nil || plan.consumed {
		return ids.ErrCatalogReplaced
	}
	current := stage.StageRead()
	if current.Owner() != plan.source.Owner() || current.Epoch() != plan.source.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, idx := range plan.primary {
		if current.Get(idx.Address()) != idx {
			return ids.ErrCatalogReplaced
		}
	}
	for _, children := range plan.children {
		for _, idx := range children.Indexes {
			if current.Get(idx.Address()) != idx {
				return ids.ErrCatalogReplaced
			}
		}
	}
	plan.consumed = true
	for i, idx := range plan.primary {
		if err := plan.children[i].Apply(stage); err != nil {
			return err
		}
		if err := index.StagePartitionIndexDrop(stage, idx, index.ConstraintDrivenDrop); err != nil {
			return err
		}
		stage.MarkDrop(plan.constraints[i], catalog.DropProvenance{Original: true})
	}
	return nil
}
