package dml

import (
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// prepareUpdateRow separates source generation from source constraints: a row
// leaving its leaf is checked as an insertion at its actual destination. The
// ordinary nonhierarchy path keeps its existing validator and buffers.
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
	// RowFor may borrow routing scratch. Publication must own the physical NEW
	// image, and RETURNING must retain a separate unchanged OLD image.
	values := state.RowFor(destination, proposal)
	if state.Layouts[destination.Address()] != nil {
		values = slices.Clone(values)
	}
	binding := named
	binding.Relation = destination
	binding.Ref = named.Ref.Domain().Ref(named.Ref.Epoch, destination.Address())
	if err := validatePhysicalInsertRow(h, binding, &named, state.Catalog.Get(destination.Namespace()).Name(), values, &generation); err != nil {
		return nil, nil, err
	}
	if segment := state.Layouts[destination.Address()]; segment != nil {
		for slot, namedSlot := range segment.Layout {
			proposal[namedSlot] = values[slot]
		}
	} else {
		copy(proposal, values)
	}
	return destination, values, nil
}

// moveUpdateRow uses the same private stage and canonical deletion identity as
// DELETE. All destination validation precedes the source deletion; only index
// revalidation is allowed after it. Finish alone publishes either both effects
// or neither. UPDATE batches never enter INSERT/COPY's deferred leaf groups.
func moveUpdateRow(h Host, named analyze.Binding, physical exec.QueryPhysicalRow, old storage.RowVersion, destination *catalog.Relation, values, proposal []sqltype.Value, evaluation *exec.Context) (bool, error) {
	if err := ClaimUniqueRow(h, physical.Leaf.Ref, old); err != nil {
		return false, err
	}
	if err := CheckViewRow(h, &named, proposal, evaluation); err != nil {
		return false, err
	}
	ref := named.Ref.Domain().Ref(named.Ref.Epoch, destination.Address())
	candidate := index.WriteCandidate{Values: values}
	if err := CheckUniqueRow(h, ref, &candidate, nil, index.WriteBatch{}); err != nil {
		return false, err
	}
	deletion := physical.Deletion()
	deletion.Moved = true
	deleted, err := h.Delete(deletion)
	if err != nil || !deleted {
		return false, err
	}
	var batch index.WriteBatch
	batch.Append(candidate)
	if err := ApplyValidated(h, ref, batch); err != nil {
		return false, err
	}
	return true, nil
}
