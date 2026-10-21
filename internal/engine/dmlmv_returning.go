package engine

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type insertReturningOutput struct {
	binding analyze.Binding
	rows    *exec.FinalRows
	overlay insertWriteOverlay
}

// This execution-private collector never escapes before executeMerge's staging
// and the request's sole Finish. It reuses one full target-first/source frame.
type mergeReturningOutput struct {
	returning *analyze.Returning
	rows      *exec.FinalRows
	frame     []sqltype.Value
}

func (o *mergeReturningOutput) project(b analyze.Binding, c *exec.Context, action parser.MergeAction, old, row, source []sqltype.Value, sourcePresent bool) error {
	if o == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.MergeReturning, evaluation.MergeAction, evaluation.MergeSourcePresent = true, action, sourcePresent
	evaluation.Row, evaluation.OldRow = o.frame, old
	evaluation.PhysicalTable = b.Ref
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.frame, row)
	copy(o.frame[b.Merge.TargetWidth:], source)
	for i, expr := range o.returning.Expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != o.rows.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized MERGE value")
		}
		o.rows.Values = append(o.rows.Values, value)
	}
	o.rows.Count++
	return nil
}

func projectForeignReturning(b analyze.Binding, output *exec.FinalRows, row, old []sqltype.Value, evaluation *exec.Context) error {
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
			return errors.New("stuntgres: invalid foreign RETURNING value")
		}
		output.Values = append(output.Values, value)
	}
	output.Count++
	return nil
}

type foreignMutationRow struct {
	row      []sqltype.Value
	identity catalog.ForeignRowIdentity
}
