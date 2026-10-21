package engine

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) beginInsertReturning(b analyze.Binding) {
	if b.Returning == nil {
		return
	}
	rows := exec.NewFinalRows(b.Returning.Schema, 0)
	rows.Output = s.output
	s.InsertReturning = &dml.InsertReturningOutput{Binding: b, Rows: rows}
}
func (s *StatementScope) projectInsertReturning(row, old []sqltype.Value, c *exec.Context) error {
	output := s.InsertReturning
	if output == nil {
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
func (s *StatementScope) finishInsertReturning() (Result, error) {
	output := s.InsertReturning
	if output.Binding.Returning.RegclassOutput && output.Rows.Count != 0 {
		if err := s.engine().captureRegclassOutput(s.op, output.Rows); err != nil {
			return Result{}, err
		}
	}
	if err := s.op.Context().Err(); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(output.Rows.Count), Rows: output.Rows}, nil
}

// Revalidate the proposal before output, never output before possible filtering.
func (s *StatementScope) acceptInsertReturning(ref ids.ObjectRef, candidate *index.WriteCandidate, evaluation *exec.Context) (bool, error) {
	row := candidate.Values
	if s.InsertConflict != nil && s.InsertConflict.Update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	proposal := *candidate
	if s.PartitionInsert != nil {
		group := s.partitionReturningGroup(ref)
		ref = group.Ref
		proposal.Values = s.partitionStoredRow(row)
		defer func() { group.Unique = s.UniqueBinding }()
	}
	for {
		admitted := true
		var err error
		if s.PartitionInsert != nil {
			err = s.checkUniqueRow(ref, &proposal, nil, index.WriteBatch{})
		} else {
			admitted, err = s.admitInsertRow(ref, &proposal, index.WriteBatch{}, evaluation)
		}
		if err != nil || !admitted {
			return false, err
		}
		accepted, retry, err := s.Writes().appendAcceptedInsert(ref, &proposal)
		if err != nil {
			return false, err
		}
		if retry {
			continue
		}
		return accepted, nil
	}
}

// The same catalog/index authority as canonical batch staging performs the
// final gate. One immutable row is appended to amortized stage storage.
func (w *WriteStage) appendAcceptedInsert(ref ids.ObjectRef, candidate *index.WriteCandidate) (bool, bool, error) {
	row := candidate.Values
	s := w.scope
	if err := s.active(); err != nil {
		return false, false, err
	}
	values := [1][]sqltype.Value{row}
	states := [1]index.WriteState{{Indexes: candidate.Indexes, Precheck: candidate.Precheck, Exclusion: candidate.Exclusion}}
	batch := index.WriteBatch{Values: values[:]}
	if candidate.Indexes != nil || candidate.Precheck != nil || candidate.Exclusion != nil {
		batch.States = states[:]
	}
	if err := s.validateIndexCandidates(ref, &batch); err != nil {
		return false, false, err
	}
	*candidate = batch.Candidate(0)
	return s.writeHost().AppendAcceptedInsert(ref, candidate)
}
func (s *StatementScope) partitionReturningGroup(ref ids.ObjectRef) *partition.InsertGroup {
	state := s.PartitionInsert
	address := state.Destination.Address()
	group := state.Groups[address]
	if group == nil {
		group = &partition.InsertGroup{Ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.Groups == nil {
			state.Groups = make(map[ids.ObjectAddress]*partition.InsertGroup)
		}
		state.Groups[address] = group
		state.Order = append(state.Order, group)
	}
	s.UniqueBinding = group.Unique
	return group
}

// Returning actions own their immutable payload in WriteStage, so callers must
// not retain a duplicate producer prefix. Upsert projects at its action seam.
func (s *StatementScope) admitInsertProducer(b analyze.Binding, candidate *index.WriteCandidate, prefix index.WriteBatch, c *exec.Context) (bool, error) {
	row := candidate.Values
	if s.InsertReturning == nil {
		return s.admitInsertRow(b.Ref, candidate, prefix, c)
	}
	if s.InsertConflict == nil || s.InsertConflict.Update == nil {
		if err := s.engine().checkViewRow(s, &b, row, c); err != nil {
			return false, err
		}
	}
	accepted, err := s.acceptInsertReturning(b.Ref, candidate, c)
	if err != nil || !accepted {
		return false, err
	}
	return false, s.projectInsertReturning(row, nil, c)
}
