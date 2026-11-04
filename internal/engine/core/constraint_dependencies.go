package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

// ConstraintDependencies captures the genuine late inherited-index program
// before removing the primary index. The owning ALTER consumes that program at
// its original late point; no descendant lookup happens after primary effects.
func (p *DropProvider) ConstraintDependencies(constraint, backing ids.ObjectAddress, mode parser.DropMode) error {
	late, err := index.PreparePartitionConstraintDrop(p.Base, p.Descriptors(), p.LiveDescriptor, constraint.ObjectID)
	if err != nil {
		return err
	}
	roots := []ids.ObjectAddress{constraint}
	if backing != (ids.ObjectAddress{}) {
		roots = append(roots, backing)
	}
	for _, idx := range late.Indexes {
		p.Observe(idx.Address())
		p.Observe(idx.SQLIndex.Parent)
		roots = append(roots, idx.Address())
	}
	plan, err := p.PrepareExpressionDependencies(roots, mode, constraint)
	if err != nil {
		return err
	}
	if backing != (ids.ObjectAddress{}) {
		if err = p.Remove(p.BaseRef(backing)); err != nil {
			return err
		}
	}
	if err = p.ApplyExpressionDependencies(plan); err != nil {
		return err
	}
	if len(late.Indexes) != 0 {
		if p.Stage != nil {
			p.Stage.Indexes().ConstraintDrops = append(p.Stage.Indexes().ConstraintDrops, late)
		} else {
			p.Prepared.LateConstraints = append(p.Prepared.LateConstraints, late)
		}
	}
	return nil
}
