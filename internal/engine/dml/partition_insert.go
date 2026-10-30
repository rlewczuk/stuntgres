package dml

import (
	"context"
	"fmt"

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

// validatePartitionInsert routes row to its leaf partition, locking a newly
// used leaf, and validates it against the leaf and the target's partition
// constraint, in that order.
func validatePartitionInsert(h Host, b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) (bool, error) {
	state := h.Command().PartitionInsert
	state.NamedRow = row
	if state.Target.Partition != nil {
		if err := validateTargetPartitionConstraint(h, b, row, evaluation); err != nil {
			return false, err
		}
	}
	input, output := h.Environments()
	destination, err := state.Route(evaluation, row, input, output)
	if err != nil {
		return false, err
	}
	ref := state.Catalog.Owner().Domain.Ref(state.Catalog.Epoch(), destination.Address())
	if state.Groups[destination.Address()] == nil {
		if err := h.AcquirePartitionLock(ref, lock.RowExclusive); err != nil {
			return false, err
		}
	}
	leafBinding := b
	leafBinding.Relation, leafBinding.Ref = destination, ref
	schema := state.Catalog.Get(destination.Namespace()).Name()
	physicalRow := state.RowFor(destination, row)
	result, err := Before(h, destination, BeforeRow{Relation: ref, Event: BeforeInsert, New: physicalRow})
	if err != nil || !result.Proceed {
		return false, err
	}
	copy(physicalRow, result.Row)
	if segment := state.Layouts[destination.Address()]; segment != nil {
		for physicalSlot, namedSlot := range segment.Layout {
			row[namedSlot] = physicalRow[physicalSlot]
		}
	} else {
		copy(row, physicalRow)
	}
	if err := validatePhysicalInsertRow(h, leafBinding, &b, schema, physicalRow, evaluation); err != nil {
		return false, err
	}
	if segment := state.Layouts[destination.Address()]; segment != nil {
		for physicalSlot, namedSlot := range segment.Layout {
			row[namedSlot] = physicalRow[physicalSlot]
		}
	} else {
		copy(row, physicalRow)
	}
	// A BEFORE change may invalidate the selected leaf; it must not reroute.
	if err := validateSelectedPartition(h, b, destination, row, evaluation); err != nil {
		return false, err
	}
	if state.Target.Partition == nil {
		if err := validateTargetPartitionConstraint(h, b, row, evaluation); err != nil {
			return false, err
		}
	}
	state.Destination, state.PhysicalRow = destination, physicalRow
	return true, nil
}

// validateSelectedPartition checks a final BEFORE image against the already
// selected leaf and its ancestors. Neither INSERT nor a moved UPDATE reroutes
// a destination trigger's replacement image.
func validateSelectedPartition(h Host, b analyze.Binding, destination *catalog.Relation, row []sqltype.Value, evaluation *exec.Context) error {
	state := h.Command().PartitionInsert
	for child := destination; child.Membership != nil; {
		parent := state.Catalog.MustRelation(child.Membership.Parent)
		accepts, err := partition.Accepts(state.Catalog, parent, child, state.RowFor(parent, row), evaluation)
		if err != nil {
			return err
		}
		if !accepts {
			err := sqlError("23514", fmt.Sprintf("new row for relation %q violates partition constraint", destination.Name())).(*sqlstate.Error)
			err.Schema, err.Table = state.Catalog.Get(destination.Namespace()).Name(), destination.Name()
			detailBinding := b
			detail, failure := h.RowDetail(&detailBinding, row, DetailConstraint)
			if failure != nil {
				return failure
			}
			err.Detail = detail
			return err
		}
		child = parent
	}
	return nil
}

func validateTargetPartitionConstraint(h Host, b analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	state := h.Command().PartitionInsert
	for child := state.Target; child.Membership != nil; {
		parent := state.Catalog.MustRelation(child.Membership.Parent)
		accepts, err := partition.Accepts(state.Catalog, parent, child, state.RowFor(parent, row), evaluation)
		if err != nil {
			return err
		}
		if !accepts {
			err := sqlError("23514", fmt.Sprintf(`new row for relation "%s" violates partition constraint`, state.Target.Name())).(*sqlstate.Error)
			err.Schema, err.Table = state.Catalog.Get(state.Target.Namespace()).Name(), state.Target.Name()
			// Copy the binding so only this failure path allocates it.
			detailBinding := b
			detail, detailErr := h.RowDetail(&detailBinding, row, DetailConstraint)
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

func checkInsertUniqueRow(ctx context.Context, h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch) error {
	command := h.Command()
	if command.PartitionInsert == nil {
		return CheckUniqueRow(ctx, h, ref, candidate, nil, prefix)
	}
	group := command.partitionInsertionGroup(ref)
	command.UniqueBinding = group.Unique
	candidate.Values = command.PartitionStoredRow(candidate.Values)
	if err := CheckUniqueRow(ctx, h, group.Ref, candidate, nil, group.Rows); err != nil {
		return err
	}
	group.Unique = command.UniqueBinding
	group.Rows.Append(*candidate)
	return nil
}

// ApplyInsertValidated stages a validated INSERT batch: an unpartitioned
// batch whole, a routed one per leaf group in first-use order. It reports
// the inserted row count.
func ApplyInsertValidated(ctx context.Context, h Host, ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	command := h.Command()
	if command.PartitionInsert == nil {
		return ApplyInsertBatch(ctx, h, ref, rows)
	}
	count := 0
	for _, group := range command.PartitionInsert.Order {
		command.UniqueBinding = group.Unique
		if err := ApplyValidated(ctx, h, group.Ref, group.Rows); err != nil {
			return 0, err
		}
		count += len(group.Rows.Values)
	}
	return count, nil
}
