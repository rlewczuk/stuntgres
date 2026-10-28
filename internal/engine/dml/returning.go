package dml

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
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
	evaluation.PhysicalTable = b.Ref
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.Frame, row)
	copy(o.Frame[b.Merge.TargetWidth:], source)
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
