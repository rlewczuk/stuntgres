package dml

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type InsertReturningOutput struct {
	Binding analyze.Binding
	Rows    *exec.FinalRows
	Overlay InsertWriteOverlay
}

// This execution-private collector never escapes before executeMerge's staging
// and the request's sole Finish. It reuses one full target-first/source frame.
type MergeReturningOutput struct {
	Returning *analyze.Returning
	Rows      *exec.FinalRows
	Frame     []sqltype.Value
}

func (o *MergeReturningOutput) Project(b analyze.Binding, c *exec.Context, action parser.MergeAction, old, row, source []sqltype.Value, sourcePresent bool) error {
	if o == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.MergeReturning, evaluation.MergeAction, evaluation.MergeSourcePresent = true, action, sourcePresent
	evaluation.Row, evaluation.OldRow = o.Frame, old
	// The action producer supplies the actual accepted physical header.
	// Never replace a child identity with the named mutation target.
	if evaluation.PhysicalTable == (ids.ObjectRef{}) {
		evaluation.PhysicalTable = b.Ref
	}
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.Frame, row)
	copy(o.Frame[b.Merge.TargetWidth:], source)
	if err := exec.CaptureReturningXmax(&evaluation, returningReadsXmax(o.Returning)); err != nil {
		return err
	}
	for i, expr := range o.Returning.Expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != o.Rows.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized MERGE value")
		}
		o.Rows.Values = append(o.Rows.Values, value)
	}
	o.Rows.Count++
	return nil
}

func ProjectReturning(h Host, b analyze.Binding, output *exec.FinalRows, row, old []sqltype.Value, evaluation *exec.Context) error {
	if output == nil {
		return nil
	}
	eval := *evaluation
	eval.Scope, eval.Frame = 1, 1
	eval.Row, eval.OldRow = row, old
	if b.Delete.Using != nil || b.Update != nil && b.Update.From != nil {
		// Keep the matched source suffix and query identity, replacing only
		// the target prefix with the provider's actual NEW/returned tuple.
		copy(evaluation.Row, row)
		eval.Row = evaluation.Row
	}
	if err := exec.CaptureReturningXmax(&eval, returningReadsXmax(b.Returning)); err != nil {
		return err
	}
	for i, expression := range b.Returning.Expressions {
		value, err := expression.Eval(&eval)
		if err != nil {
			return err
		}
		if value.Type() == nil || value.Type() != output.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized RETURNING value")
		}
		output.Values = append(output.Values, value)
	}
	output.Count++
	return h.Command().CapturedRow()
}

func finishMutationResult(h Host, b analyze.Binding, output *exec.FinalRows, count int64) (Outcome, error) {
	if output != nil && b.Returning.RegclassOutput && count != 0 {
		if err := h.CaptureRegclassOutput(output); err != nil {
			output.Close()
			return Outcome{}, err
		}
	}
	if err := h.Context().Err(); err != nil {
		if output != nil {
			output.Close()
		}
		return Outcome{}, err
	}
	return Outcome{Count: count, Rows: output}, nil
}

type ForeignMutationRow struct {
	Row      []sqltype.Value
	Identity catalog.ForeignRowIdentity
}

// DELETE RETURNING observes the admitted deletion header, not the older source
// capture. The private delete overlay is the writer's exact-version authority.
func captureDeletedHeader(h Host, ref ids.ObjectRef, current storage.RowVersion, evaluation *exec.Context) error {
	deletes := h.PrivateRows(ref.Address).StagedDeletes
	if deletes == nil {
		return errors.New("stuntgres: missing admitted deletion header")
	}
	deleted, ok := deletes.Lookup(current.Row, current.Version)
	if !ok || deleted.Version != current.Version {
		return errors.New("stuntgres: mismatched admitted deletion header")
	}
	current.DeleteFullXID = deleted.EndFullXID
	current.RepairStamp = deleted.RepairStamp
	if current.RepairStamp != 0 {
		current.Frozen, current.ForcedFrozen = true, true
	}
	evaluation.PhysicalTable, evaluation.PhysicalVersion = ref, current
	evaluation.OldPhysicalTable, evaluation.OldPhysicalVersion = ref, current
	return nil
}

// Source system attributes already retain their own captures. Only target
// attributes demand a newly admitted version from the mutation stage.
func returningHasPhysicalHeaders(returning *analyze.Returning) bool {
	if returning == nil {
		return false
	}
	found := false
	var visit func(expr.Expr)
	visit = func(value expr.Expr) {
		plan.WalkExpression(value, func(value expr.Expr) {
			if n, ok := value.(plan.SystemAttributeExpr); ok && n.Scope == 1 && n.Frame == 1 && n.QueryOccurrence == 0 {
				found = true
			}
			if query, ok := value.(plan.SubqueryExpr); ok && !found && !query.CTEEdge && query.QueryPlan != nil {
				plan.VisitExecutableQuery(query.QueryPlan.Root, func(plan.Node) {}, visit)
			}
		})
	}
	for _, value := range returning.Expressions {
		visit(value)
	}
	return found
}

func returningReadsXmax(returning *analyze.Returning) bool {
	if returning == nil {
		return false
	}
	found := false
	var visit func(expr.Expr)
	visit = func(value expr.Expr) {
		plan.WalkExpression(value, func(value expr.Expr) {
			if n, ok := value.(plan.SystemAttributeExpr); ok && n.SubID == -4 {
				found = true
			}
			if n, ok := value.(plan.SubqueryExpr); ok && !n.CTEEdge && n.QueryPlan != nil {
				plan.VisitExecutableQuery(n.QueryPlan.Root, func(plan.Node) {}, visit)
			}
		})
	}
	for _, value := range returning.Expressions {
		visit(value)
	}
	return found
}
