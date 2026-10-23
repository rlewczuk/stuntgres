package dml

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// BeginInsertReturning installs the active command's incremental INSERT stage
// and optional RETURNING output. Triggers require accepted rows to be visible
// to later invocations even without a RETURNING clause.
func BeginInsertReturning(h Host, b analyze.Binding) {
	if b.Returning == nil {
		beforeInsert := false
		if state := h.Command().PartitionInsert; state != nil {
			beforeInsert = state.BeforeInsert
		} else {
			for _, definition := range stored.TriggersOf(b.Relation) {
				if definition.Fires(stored.TriggerInsert, nil) {
					beforeInsert = true
					break
				}
			}
		}
		if !beforeInsert {
			return
		}
	}
	var schema []catalog.Column
	if b.Returning != nil {
		schema = b.Returning.Schema
	}
	rows := exec.NewFinalRows(schema, 0)
	_, rows.Output = h.Environments()
	h.Command().InsertReturning = &InsertReturningOutput{Binding: b, Rows: rows}
}
func projectInsertReturning(h Host, row, old []sqltype.Value, c *exec.Context) error {
	output := h.Command().InsertReturning
	if output == nil {
		return nil
	}
	if output.Binding.Returning == nil {
		output.Rows.Count++
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.Row, evaluation.OldRow = row, old
	for i, expr := range output.Binding.Returning.Expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.Rows.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized INSERT value")
		}
		output.Rows.Values = append(output.Rows.Values, value)
	}
	output.Rows.Count++
	return nil
}
func finishInsertReturning(h Host) (Outcome, error) {
	output := h.Command().InsertReturning
	if output.Binding.Returning != nil && output.Binding.Returning.RegclassOutput && output.Rows.Count != 0 {
		if err := h.CaptureRegclassOutput(output.Rows); err != nil {
			return Outcome{}, err
		}
	}
	if err := h.Context().Err(); err != nil {
		return Outcome{}, err
	}
	if output.Binding.Returning == nil {
		count := output.Rows.Count
		output.Rows.Close()
		return Outcome{Count: int64(count)}, nil
	}
	return Outcome{Count: int64(output.Rows.Count), Rows: output.Rows}, nil
}

// Revalidate the proposal before output, never output before possible filtering.
func acceptInsertReturning(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, evaluation *exec.Context) (bool, error) {
	command := h.Command()
	row := candidate.Values
	if command.InsertConflict != nil && command.InsertConflict.Update != nil {
		return false, admitInsertConflictUpdate(h, ref, candidate, evaluation)
	}
	proposal := *candidate
	if command.PartitionInsert != nil {
		group := command.partitionReturningGroup(ref)
		ref = group.Ref
		proposal.Values = command.PartitionStoredRow(row)
		defer func() { group.Unique = command.UniqueBinding }()
	}
	for {
		admitted := true
		var err error
		if command.PartitionInsert != nil {
			err = CheckUniqueRow(h, ref, &proposal, nil, index.WriteBatch{})
		} else {
			admitted, err = AdmitInsertRow(h, ref, &proposal, index.WriteBatch{}, evaluation)
		}
		if err != nil || !admitted {
			return false, err
		}
		accepted, retry, err := AppendAcceptedInsert(h, ref, &proposal)
		if err != nil {
			return false, err
		}
		if retry {
			continue
		}
		return accepted, nil
	}
}

// AppendAcceptedInsert stages one validated RETURNING row under the same
// catalog and index authority as canonical batch staging, which performs
// the final gate. stale reports a moved unique binding: the caller
// revalidates and retries.
func AppendAcceptedInsert(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate) (accepted, stale bool, err error) {
	row := candidate.Values
	if err := h.Active(); err != nil {
		return false, false, err
	}
	values := [1][]sqltype.Value{row}
	states := [1]index.WriteState{{Indexes: candidate.Indexes, Precheck: candidate.Precheck, Exclusion: candidate.Exclusion}}
	batch := index.WriteBatch{Values: values[:]}
	if candidate.Indexes != nil || candidate.Precheck != nil || candidate.Exclusion != nil {
		batch.States = states[:]
	}
	if err := ValidateIndexCandidates(h, ref, &batch); err != nil {
		return false, false, err
	}
	*candidate = batch.Candidate(0)
	return h.AppendAcceptedInsert(ref, *candidate)
}

// AdmitInsertProducer admits one produced INSERT row. Returning actions own
// their immutable payload in the write stage, so callers must not retain a
// duplicate producer prefix (it reports false); upsert projects at its action
// seam.
func AdmitInsertProducer(h Host, b analyze.Binding, candidate *index.WriteCandidate, prefix index.WriteBatch, c *exec.Context) (bool, error) {
	command := h.Command()
	row := candidate.Values
	if command.InsertReturning == nil {
		return AdmitInsertRow(h, b.Ref, candidate, prefix, c)
	}
	if command.InsertConflict == nil || command.InsertConflict.Update == nil {
		if err := CheckViewRow(h, &b, row, c); err != nil {
			return false, err
		}
	}
	accepted, err := acceptInsertReturning(h, b.Ref, candidate, c)
	if err != nil || !accepted {
		return false, err
	}
	return false, projectInsertReturning(h, row, nil, c)
}
