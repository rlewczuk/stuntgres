package dml

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CheckViewRow checks the view CHECK OPTIONs of b's view write against row.
// Called only for admitted, finalized rows, after physical validation or the
// logical INSTEAD chain; NULL-suppressed occurrences never reach this boundary.
// A stack copy separates NEW from UPDATE FROM's OLD/source/RETURNING frame.
func CheckViewRow(h Host, b *analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	if b.ViewWrite == nil || len(view.ViewWriteOf(b.ViewWrite).Checks) == 0 {
		return nil
	}
	newFrame := *evaluation
	newFrame.Row = row
	newFrame.Scope, newFrame.Frame = 1, 1
	for i := len(view.ViewWriteOf(b.ViewWrite).Checks) - 1; i >= 0; i-- {
		group := &view.ViewWriteOf(b.ViewWrite).Checks[i]
		for j := len(group.Predicates) - 1; j >= 0; j-- {
			if err := exec.EvaluationContext(&newFrame); err != nil {
				return err
			}
			value, err := group.Predicates[j].Eval(&newFrame)
			if err != nil {
				return err
			}
			truth, err := plan.TruthOf(value)
			if err != nil {
				return err
			}
			if truth != plan.True {
				// Copy the binding so only this failure path allocates it.
				detailBinding := *b
				detail, err := h.RowDetail(&detailBinding, row, DetailViewCheck)
				if err != nil {
					return err
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "44000", Message: fmt.Sprintf("new row violates check option for view \"%s\"", group.Relation.Name()), Detail: detail}
			}
		}
	}
	return exec.EvaluationContext(&newFrame)
}

// ValidateInsertRow owns the one semantic INSERT BEFORE boundary, routing a
// partitioned row first. Revalidation uses validatePhysicalInsertRow directly.
func ValidateInsertRow(h Host, b analyze.Binding, schema string, row []sqltype.Value, evaluation *exec.Context) (bool, error) {
	if h.Command().PartitionInsert != nil {
		return validatePartitionInsert(h, b, row, evaluation)
	}
	result, err := Before(h, b.Relation, BeforeRow{Relation: b.Ref, Event: BeforeInsert, New: row})
	if err != nil || !result.Proceed {
		return false, err
	}
	copy(row, result.Row)
	return true, validatePhysicalInsertRow(h, b, &b, schema, row, evaluation)
}
func validatePhysicalInsertRow(h Host, b analyze.Binding, named *analyze.Binding, schema string, row []sqltype.Value, evaluation *exec.Context) error {
	if err := evaluation.Context.Err(); err != nil {
		return err
	}
	if err := table.EvaluateGenerations(evaluation.Context, b.Relation, row, evaluation); err != nil {
		return err
	}
	rls := &h.Command().RLS
	proposal := row
	if rls.New != nil && named.Relation != b.Relation {
		state := h.Command().PartitionInsert
		if segment := state.Layouts[b.Relation.Address()]; segment != nil {
			proposal = state.NamedRow
			// Routing admitted this physical-slot -> named-slot layout once.
			// Reuse the private named row for finalized leaf generations.
			for physicalSlot, namedSlot := range segment.Layout {
				proposal[namedSlot] = row[physicalSlot]
			}
		}
	}
	if err := rls.CheckProposal(proposal, evaluation); err != nil {
		return err
	}
	violation, err := table.PhysicalRowViolation(b.Relation, row, evaluation)
	if err != nil || violation == nil {
		return err
	}
	// No-program success never needs a named proposal. A real diagnostic does:
	// the host renders NamedRow, including finalized leaf-generated values.
	if rls.New == nil && named.Relation != b.Relation {
		state := h.Command().PartitionInsert
		if segment := state.Layouts[b.Relation.Address()]; segment != nil {
			for physicalSlot, namedSlot := range segment.Layout {
				state.NamedRow[namedSlot] = row[physicalSlot]
			}
		}
	}
	// Copy the binding so only this failure path allocates it.
	detailBinding := *named
	detail, err := h.RowDetail(&detailBinding, row, DetailConstraint)
	if err != nil {
		return err
	}
	return violation.Diagnostic(schema, detail)
}

// RowConstraintError is the NOT NULL (column) or CHECK (check) violation of
// row in b's relation, with the failing-row detail the actor may see.
func RowConstraintError(h Host, b analyze.Binding, schema, column, check string, row []sqltype.Value) error {
	detail, err := h.RowDetail(&b, row, DetailConstraint)
	if err != nil {
		return err
	}
	if check != "" {
		return table.CheckDetailError(schema, b.Relation.Name(), check, detail)
	}
	return table.NotNullDetailError(schema, b.Relation.Name(), column, detail)
}
