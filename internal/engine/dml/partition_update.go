package dml

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// prepareUpdateRow separates source generation from source constraints: a row
// leaving its leaf is checked as an insertion after the movement DELETE and
// INSERT hooks. The ordinary nonhierarchy path keeps its existing validator.
func prepareUpdateRow(h Host, named analyze.Binding, physical exec.QueryPhysicalRow, schema string, row, proposal []sqltype.Value, evaluation *exec.Context) (*catalog.Relation, []sqltype.Value, error) {
	state := h.Command().PartitionInsert
	if state == nil {
		return nil, row, validateInheritedMutationRow(h, named, physical, schema, row, proposal, evaluation)
	}
	state.NamedRow = proposal
	generation := *evaluation
	if err := table.EvaluateGenerations(evaluation.Context, physical.Leaf.Relation, row, &generation); err != nil {
		return nil, nil, err
	}
	if physical.Segment != nil && physical.Segment.Layout != nil {
		for i, slot := range physical.Segment.Layout {
			proposal[i] = row[slot]
		}
	}
	accepts := true
	for child := physical.Leaf.Relation; child.Membership != nil; {
		parent := state.Catalog.MustRelation(child.Membership.Parent)
		ok, err := partition.Accepts(state.Catalog, parent, child, state.RowFor(parent, proposal), &generation)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			accepts = false
			break
		}
		child = parent
	}
	if accepts || state.Target.Partition == nil {
		if err := validateInheritedMutationConstraints(h, named, physical, schema, row, proposal, &generation); err != nil {
			return nil, nil, err
		}
		if !accepts {
			if err := validateTargetPartitionConstraint(h, named, proposal, &generation); err != nil {
				return nil, nil, err
			}
		}
		return nil, row, nil
	}
	if err := validateTargetPartitionConstraint(h, named, proposal, &generation); err != nil {
		return nil, nil, err
	}
	input, output := h.Environments()
	destination, err := state.Route(&generation, proposal, input, output)
	if err != nil {
		return nil, nil, err
	}
	ref := named.Ref.Domain().Ref(named.Ref.Epoch, destination.Address())
	if err := h.AcquirePartitionLock(ref, lock.RowExclusive); err != nil {
		return nil, nil, err
	}
	// RowFor may borrow routing scratch. Publication must own the physical NEW
	// image, and RETURNING must retain a separate unchanged OLD image.
	values := state.RowFor(destination, proposal)
	if state.Layouts[destination.Address()] != nil {
		values = slices.Clone(values)
	}
	return destination, values, nil
}

// moveUpdateRow preserves the movement DELETE/INSERT boundary in the existing
// statement stage. A DELETE veto keeps OLD; an INSERT veto keeps the staged
// deletion but produces no UPDATE count or RETURNING row. Finish alone publishes
// effects, so any later destination failure also rolls the source deletion back.
func moveUpdateRow(h Host, named analyze.Binding, physical exec.QueryPhysicalRow, old storage.RowVersion, destination *catalog.Relation, values, proposal []sqltype.Value, evaluation *exec.Context) (bool, error) {
	before, err := Before(h, physical.Leaf.Relation, BeforeRow{Relation: physical.Leaf.Ref, Event: BeforeDelete, Old: old.Values})
	if err != nil || !before.Proceed {
		return false, err
	}
	if err := ClaimUniqueRow(h, physical.Leaf.Ref, old); err != nil {
		return false, err
	}
	deletion := physical.Deletion()
	deletion.Moved = true
	deleted, err := h.Delete(deletion)
	if err != nil || !deleted {
		return false, err
	}
	// Destination BEFORE INSERT observes the source deletion, just as a
	// volatile function called by the physical INSERT would.
	ref := named.Ref.Domain().Ref(named.Ref.Epoch, destination.Address())
	before, err = Before(h, destination, BeforeRow{Relation: ref, Event: BeforeInsert, New: values})
	if err != nil || !before.Proceed {
		return false, err
	}
	copy(values, before.Row)
	state := h.Command().PartitionInsert
	project := func() {
		if segment := state.Layouts[destination.Address()]; segment != nil {
			for slot, namedSlot := range segment.Layout {
				proposal[namedSlot] = values[slot]
			}
		} else {
			copy(proposal, values)
		}
	}
	project()
	binding := named
	binding.Relation, binding.Ref = destination, ref
	if err := validatePhysicalInsertRow(h, binding, &named, state.Catalog.Get(destination.Namespace()).Name(), values, evaluation); err != nil {
		return false, err
	}
	project()
	if err := validateSelectedPartition(h, named, destination, proposal, evaluation); err != nil {
		return false, err
	}
	if err := CheckViewRow(h, &named, proposal, evaluation); err != nil {
		return false, err
	}
	candidate := index.WriteCandidate{Values: values}
	if err := CheckUniqueRow(h, ref, &candidate, nil, index.WriteBatch{}); err != nil {
		return false, err
	}
	var batch index.WriteBatch
	batch.Append(candidate)
	if err := ApplyValidated(h, ref, batch); err != nil {
		return false, err
	}
	return true, nil
}
