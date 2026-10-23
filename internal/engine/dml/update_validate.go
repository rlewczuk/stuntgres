package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// ValidateMutationRow is the generation, NOT NULL and CHECK validation of an
// UPDATE row; public UPDATE and admitted RI assignment share it.
func ValidateMutationRow(h Host, b analyze.Binding, schema string, row []sqltype.Value, eval *exec.Context) error {
	return validateMutationRow(h, b, schema, row, eval, false)
}

func validateMutationRow(h Host, b analyze.Binding, schema string, row []sqltype.Value, eval *exec.Context, public bool) error {
	generationEval := *eval
	if err := table.EvaluateGenerations(eval.Context, b.Relation, row, &generationEval); err != nil {
		return err
	}
	return validateMutationConstraints(h, b, schema, row, &generationEval, public)
}

func validateMutationConstraints(h Host, b analyze.Binding, schema string, row []sqltype.Value, eval *exec.Context, public bool) error {
	if public {
		if err := h.Command().RLS.CheckProposal(row, eval); err != nil {
			return err
		}
	}
	for i, column := range b.Relation.Columns {
		if column.Nullable {
			continue
		}
		value, err := table.LogicalColumnValue(b.Relation, i, row, eval)
		if err != nil {
			return err
		}
		if value.IsNull() {
			return RowConstraintError(h, b, schema, column.Name, "", row)
		}
	}
	if len(b.Relation.Constraints.Check) != 0 {
		constraint, err := table.ValidateCheckRow(b.Relation, row, eval)
		if err != nil {
			return err
		}
		if constraint != nil {
			return RowConstraintError(h, b, schema, "", constraint.Name, row)
		}
	}
	return nil
}

// Physical generation/checks use the full physical proposal; diagnostic values
// and SELECT rights are projected back to the named mutation target.
func validateInheritedMutationRow(h Host, named analyze.Binding, physical exec.QueryPhysicalRow, schema string, row, proposal []sqltype.Value, eval *exec.Context) error {
	generationEval := *eval
	if err := table.EvaluateGenerations(eval.Context, physical.Leaf.Relation, row, &generationEval); err != nil {
		return err
	}
	return validateInheritedMutationConstraints(h, named, physical, schema, row, proposal, &generationEval)
}

func validateInheritedMutationConstraints(h Host, named analyze.Binding, physical exec.QueryPhysicalRow, schema string, row, proposal []sqltype.Value, eval *exec.Context) error {
	if physical.Leaf.Ref == named.Ref {
		return validateMutationConstraints(h, named, schema, row, eval, true)
	}
	relation := physical.Leaf.Relation
	// OLD may still borrow the segment's mapping scratch for RETURNING. Reuse
	// the private logical replacement for finalized NEW instead of overwriting
	// that borrowed frame, and project only when an actual program needs it.
	if h.Command().RLS.New != nil && physical.Segment != nil && physical.Segment.Layout != nil {
		for i, slot := range physical.Segment.Layout {
			proposal[i] = row[slot]
		}
	}
	if err := h.Command().RLS.CheckProposal(proposal, eval); err != nil {
		return err
	}
	diagnostic := func(column, check string) error {
		// A copy keeps named off the heap on the success path.
		failing := named
		detail, err := h.RowDetail(&failing, physical.Values(storage.RowVersion{Values: row}), DetailConstraint)
		if err != nil {
			return err
		}
		physicalSchema := schema
		if name, ok := h.SchemaName(relation.Namespace()); ok {
			physicalSchema = name
		}
		if check != "" {
			return table.CheckDetailError(physicalSchema, relation.Name(), check, detail)
		}
		return table.NotNullDetailError(physicalSchema, relation.Name(), column, detail)
	}
	for i, column := range relation.Columns {
		if !column.Nullable {
			value, err := table.LogicalColumnValue(relation, i, row, eval)
			if err != nil {
				return err
			}
			if value.IsNull() {
				return diagnostic(column.Name, "")
			}
		}
	}
	if constraint, err := table.ValidateCheckRow(relation, row, eval); err != nil {
		return err
	} else if constraint != nil {
		return diagnostic("", constraint.Name)
	}
	return nil
}
