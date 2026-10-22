package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CheckViewRow checks the view CHECK OPTIONs of b's view write against row.
// Called only for admitted, finalized rows, after base constraints/uniqueness.
// A stack copy separates NEW from UPDATE FROM's OLD/source/RETURNING frame.
func CheckViewRow(h dml.Host, b *analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
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
				detail, err := h.RowDetail(&detailBinding, row, dml.DetailViewCheck)
				if err != nil {
					return err
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "44000", Message: fmt.Sprintf("new row violates check option for view \"%s\"", group.Relation.Name()), Detail: detail}
			}
		}
	}
	return exec.EvaluationContext(&newFrame)
}

// ValidateInsertRow routes a partitioned INSERT row, or validates row against
// b's own relation.
func ValidateInsertRow(h dml.Host, b analyze.Binding, schema string, row []sqltype.Value, evaluation *exec.Context) error {
	if h.Command().PartitionInsert != nil {
		return validatePartitionInsert(h, b, row, evaluation)
	}
	return validatePhysicalInsertRow(h, b, &b, schema, row, evaluation)
}
func validatePhysicalInsertRow(h dml.Host, b analyze.Binding, named *analyze.Binding, schema string, row []sqltype.Value, evaluation *exec.Context) error {
	if err := evaluation.Context.Err(); err != nil {
		return err
	}
	if err := table.EvaluateGenerations(evaluation.Context, b.Relation, row, evaluation); err != nil {
		return err
	}
	rls := &h.Command().RLS
	if err := access.RowSecurityProposal(rls.Proposal, rls.ProposalTable); err != nil {
		return err
	}
	violation, err := table.PhysicalRowViolation(b.Relation, row, evaluation)
	if err != nil || violation == nil {
		return err
	}
	// Copy the binding so only this failure path allocates it.
	detailBinding := *named
	detail, err := h.RowDetail(&detailBinding, row, dml.DetailConstraint)
	if err != nil {
		return err
	}
	return violation.Diagnostic(schema, detail)
}

// RowConstraintError is the NOT NULL (column) or CHECK (check) violation of
// row in b's relation, with the failing-row detail the actor may see.
func RowConstraintError(h dml.Host, b analyze.Binding, schema, column, check string, row []sqltype.Value) error {
	detail, err := h.RowDetail(&b, row, dml.DetailConstraint)
	if err != nil {
		return err
	}
	if check != "" {
		return table.CheckDetailError(schema, b.Relation.Name(), check, detail)
	}
	return table.NotNullDetailError(schema, b.Relation.Name(), column, detail)
}
