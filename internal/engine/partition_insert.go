package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) capturePartitionInsertLocked(e *Engine, auth AuthorizationView, target *catalog.Relation) error {
	if !catalog.HierarchyRelation(target) {
		return nil
	}
	state := &partition.InsertState{Catalog: auth.Catalog, Authority: auth.View, Env: auth, Target: target}
	var captureLayouts func(*catalog.Relation) error
	captureLayout := func(d *catalog.Relation) error {
		if s.output.RegTS == nil && d.Partition != nil {
			for _, key := range d.Partition.Keys {
				if key.Usage.Type.ContainsRegTS() {
					if err := e.captureCommentSnapshotLocked(s); err != nil {
						return err
					}
					break
				}
			}
		}
		layout, err := catalog.InheritanceQueryLayout(d, target)
		if err != nil {
			return err
		}
		if layout != nil {
			if state.Layouts == nil {
				state.Layouts = make(map[ids.ObjectAddress]*exec.InheritanceQuerySegment)
			}
			state.Layouts[d.Address()] = &exec.InheritanceQuerySegment{Layout: layout, Scratch: make([]sqltype.Value, len(layout))}
		}
		return nil
	}
	captureLayouts = func(d *catalog.Relation) error {
		if err := captureLayout(d); err != nil {
			return err
		}
		if d.Partition != nil {
			for _, address := range d.Partition.Children {
				if err := captureLayouts(auth.Catalog.MustRelation(address)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := captureLayouts(target); err != nil {
		return err
	}
	for child := target; child.Membership != nil; {
		parent := auth.Catalog.MustRelation(child.Membership.Parent)
		if err := captureLayout(parent); err != nil {
			return err
		}
		child = parent
	}
	if err := partition.PhysicalDescendants(auth.Catalog, target, func(leaf *catalog.Relation) error {
		if err := e.captureCheckCommentSnapshotLocked(s, leaf); err != nil {
			return err
		}
		return e.captureCheckVisibilitySnapshotLocked(s, leaf)
	}); err != nil {
		return err
	}
	s.PartitionInsert = state
	return nil
}
