package engine

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type insertReturningOutput struct {
	binding analyze.Binding
	rows    *exec.FinalRows
	overlay insertWriteOverlay
}

func (s *StatementScope) beginInsertReturning(b analyze.Binding) {
	if b.Returning == nil {
		return
	}
	rows := exec.NewFinalRows(b.Returning.Schema, 0)
	rows.Output = s.output
	s.insertReturning = &insertReturningOutput{binding: b, rows: rows}
}
func (s *StatementScope) projectInsertReturning(row, old []sqltype.Value, c *exec.Context) error {
	output := s.insertReturning
	if output == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.Row, evaluation.OldRow = row, old
	for i, expr := range output.binding.Returning.Expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.rows.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized INSERT value")
		}
		output.rows.Values = append(output.rows.Values, value)
	}
	output.rows.Count++
	return nil
}
func (s *StatementScope) finishInsertReturning() (Result, error) {
	output := s.insertReturning
	if output.binding.Returning.RegclassOutput && output.rows.Count != 0 {
		if err := s.engine().captureRegclassOutput(s.op, output.rows); err != nil {
			return Result{}, err
		}
	}
	if err := s.op.Context().Err(); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(output.rows.Count), Rows: output.rows}, nil
}

// Revalidate the proposal before output, never output before possible filtering.
func (s *StatementScope) acceptInsertReturning(ref ids.ObjectRef, candidate *index.WriteCandidate, evaluation *exec.Context) (bool, error) {
	row := candidate.Values
	if s.insertConflict != nil && s.insertConflict.Update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	proposal := *candidate
	if s.partitionInsert != nil {
		group := s.partitionReturningGroup(ref)
		ref = group.Ref
		proposal.Values = s.partitionStoredRow(row)
		defer func() { group.Unique = s.uniqueBinding }()
	}
	for {
		admitted := true
		var err error
		if s.partitionInsert != nil {
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
	s.owner.Mu.Lock()
	defer s.owner.Mu.Unlock()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted {
		return false, false, errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.catalog.Domain {
		return false, false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.owner.Ref.Epoch() {
		return false, false, ids.ErrCatalogReplaced
	}
	catalog := e.privateCatalogLocked(s.owner.Catalog)
	if s.uniqueBinding == nil || s.uniqueBinding.Catalog != catalog.State() {
		return false, true, nil
	}
	parent, ok := catalog.Relation(ref.Address)
	if !ok || !index.SameRelationRowShape(s.uniqueBinding.Parent, parent) {
		return false, false, ids.ErrCatalogReplaced
	}
	if e.privateStorageLocked(s.owner.Catalog, ref.Address) == nil {
		return false, false, errors.New("stuntgres: relation storage is missing")
	}
	if len(row) != len(parent.Columns) {
		return false, false, errors.New("stuntgres: invalid staged row width")
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return false, false, err
	}
	auth.Catalog = catalog
	auth.Recapture(auth.Catalog)
	b := s.insertReturning.binding
	if b.ViewWrite == nil && s.partitionInsert == nil {
		b.Ref, b.Relation = ref, parent
	}
	if err = analyze.CheckBinding(auth, b, &parser.Insert{}); err != nil {
		return false, false, err
	}
	var version storage.RowVersion
	if candidate.Exclusion != nil {
		version = *candidate.Exclusion
	} else {
		if e.storage.RowFrontier() == ^uint64(0) || e.storage.VersionFrontier() == ^uint64(0) {
			return false, false, errors.New("stuntgres: row or version identity exhausted")
		}
		version = storage.RowVersion{Row: e.storage.AllocateRowLocked(), Version: e.storage.AllocateVersionLocked(), Values: row}
	}
	version.IndexAdmission = catalog.State()
	version.IndexOutputs = candidate.Indexes
	if e.visibility.RelevantSerializableLocked(s.txRef) != nil {
		events := [1]visibility.SerializableWrite{{Row: version.Row, Version: version.Version}}
		if err = e.serializableWriteLocked(s, ref, events[:]); err != nil {
			return false, false, err
		}
	}
	if w.rows == nil {
		w.rows = make(map[ids.ObjectAddress][]storage.RowVersion)
	}
	w.rows[ref.Address] = append(w.rows[ref.Address], version)
	return true, false, nil
}
func (s *StatementScope) partitionReturningGroup(ref ids.ObjectRef) *partition.InsertGroup {
	state := s.partitionInsert
	address := state.destination.Address()
	group := state.groups[address]
	if group == nil {
		group = &partition.InsertGroup{Ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.groups == nil {
			state.groups = make(map[ids.ObjectAddress]*partition.InsertGroup)
		}
		state.groups[address] = group
		state.order = append(state.order, group)
	}
	s.uniqueBinding = group.Unique
	return group
}

// Returning actions own their immutable payload in WriteStage, so callers must
// not retain a duplicate producer prefix. Upsert projects at its action seam.
func (s *StatementScope) admitInsertProducer(b analyze.Binding, candidate *index.WriteCandidate, prefix index.WriteBatch, c *exec.Context) (bool, error) {
	row := candidate.Values
	if s.insertReturning == nil {
		return s.admitInsertRow(b.Ref, candidate, prefix, c)
	}
	if s.insertConflict == nil || s.insertConflict.Update == nil {
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
