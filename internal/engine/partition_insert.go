package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqlstate"
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

func (s *StatementScope) validatePartitionInsert(b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	state := s.PartitionInsert
	state.NamedRow = row
	if state.Target.Partition != nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	destination, err := state.Route(evaluation, row, s.input, s.output)
	if err != nil {
		return err
	}
	ref := state.Catalog.Owner().Domain.Ref(state.Catalog.Epoch(), destination.Address())
	if state.Groups[destination.Address()] == nil {
		if err := s.acquirePartitionLock(ref, lock.RowExclusive); err != nil {
			return err
		}
	}
	leafBinding := b
	leafBinding.Relation, leafBinding.Ref = destination, ref
	schema := state.Catalog.Get(destination.Namespace()).Name()
	physicalRow := state.RowFor(destination, row)
	if err := s.engine().validatePhysicalInsertRow(s, leafBinding, &b, schema, physicalRow, evaluation); err != nil {
		return err
	}
	if state.Target.Partition == nil {
		if err := s.validateTargetPartitionConstraint(b, row, evaluation); err != nil {
			return err
		}
	}
	state.Destination = destination
	state.PhysicalRow = physicalRow
	return nil
}

func (s *StatementScope) validateTargetPartitionConstraint(b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	state := s.PartitionInsert
	for child := state.Target; child.Membership != nil; {
		parent := state.Catalog.MustRelation(child.Membership.Parent)
		accepts, err := partition.Accepts(state.Catalog, parent, child, state.RowFor(parent, row), evaluation)
		if err != nil {
			return err
		}
		if !accepts {
			err := sqlError("23514", fmt.Sprintf(`new row for relation "%s" violates partition constraint`, state.Target.Name())).(*sqlstate.Error)
			err.Schema, err.Table = state.Catalog.Get(state.Target.Namespace()).Name(), state.Target.Name()
			detail, detailErr := s.engine().rowConstraintDetail(s, &b, row)
			if detailErr != nil {
				return detailErr
			}
			err.Detail = detail
			return err
		}
		child = parent
	}
	return nil
}

// Input tuples already own their payload. Identity layouts borrow them. A
// reordered tuple can reuse that payload when there is no logical RETURNING
// consumer; otherwise storage must own a separate immutable physical tuple.
func (s *StatementScope) partitionStoredRow(row []sqltype.Value) []sqltype.Value {
	state := s.PartitionInsert
	if state.Layouts[state.Destination.Address()] == nil {
		return row
	}
	if s.InsertReturning != nil {
		return slices.Clone(state.PhysicalRow)
	}
	copy(row, state.PhysicalRow)
	return row
}

func (s *StatementScope) partitionInsertionGroup(ref ids.ObjectRef) *partition.InsertGroup {
	state := s.PartitionInsert
	address := state.Destination.Address()
	group := state.Groups[address]
	if group == nil {
		group = &partition.InsertGroup{Ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.Groups == nil {
			state.Groups = make(map[ids.ObjectAddress]*partition.InsertGroup)
		}
		state.Groups[address] = group
		state.Order = append(state.Order, group)
	}
	return group
}

func (s *StatementScope) checkInsertUniqueRow(ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch) error {
	if s.PartitionInsert == nil {
		return checkUniqueRow(s.writeHost(), ref, candidate, nil, prefix)
	}
	group := s.partitionInsertionGroup(ref)
	s.UniqueBinding = group.Unique
	candidate.Values = s.partitionStoredRow(candidate.Values)
	if err := checkUniqueRow(s.writeHost(), group.Ref, candidate, nil, group.Rows); err != nil {
		return err
	}
	group.Unique = s.UniqueBinding
	group.Rows.Append(*candidate)
	return nil
}

func (s *StatementScope) applyInsertValidated(ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	if s.PartitionInsert == nil {
		return s.Writes().applyInsertBatch(ref, rows)
	}
	count := 0
	for _, group := range s.PartitionInsert.Order {
		s.UniqueBinding = group.Unique
		if err := applyValidated(s.writeHost(), group.Ref, group.Rows); err != nil {
			return 0, err
		}
		count += len(group.Rows.Values)
	}
	return count, nil
}
