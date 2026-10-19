package engine

import (
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type insertReturningOutput struct {
	binding binding
	rows    *finalRows
	overlay insertWriteOverlay
}

func (s *StatementScope) beginInsertReturning(b binding) {
	if b.returning == nil {
		return
	}
	rows := newFinalRows(b.returning.schema, 0)
	rows.output = s.output
	s.insertReturning = &insertReturningOutput{binding: b, rows: rows}
}
func (s *StatementScope) projectInsertReturning(row, old []sqltype.Value, c *EvalContext) error {
	output := s.insertReturning
	if output == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.Row, evaluation.OldRow = row, old
	for i, expr := range output.binding.returning.expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.rows.schema[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized INSERT value")
		}
		output.rows.values = append(output.rows.values, value)
	}
	output.rows.count++
	return nil
}
func (s *StatementScope) finishInsertReturning() (Result, error) {
	output := s.insertReturning
	if output.binding.returning.regclassOutput && output.rows.count != 0 {
		if err := s.op.session.Engine().captureRegclassOutput(s.op, output.rows); err != nil {
			return Result{}, err
		}
	}
	if err := s.op.Context().Err(); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(output.rows.count), Rows: output.rows}, nil
}

// Revalidate the proposal before output, never output before possible filtering.
func (s *StatementScope) acceptInsertReturning(ref ids.ObjectRef, candidate *indexWriteCandidate, evaluation *EvalContext) (bool, error) {
	row := candidate.values
	if s.insertConflict != nil && s.insertConflict.update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	proposal := *candidate
	if s.partitionInsert != nil {
		group := s.partitionReturningGroup(ref)
		ref = group.ref
		proposal.values = s.partitionStoredRow(row)
		defer func() { group.unique = s.uniqueBinding }()
	}
	for {
		admitted := true
		var err error
		if s.partitionInsert != nil {
			err = s.checkUniqueRow(ref, &proposal, nil, indexWriteBatch{})
		} else {
			admitted, err = s.admitInsertRow(ref, &proposal, indexWriteBatch{}, evaluation)
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
func (w *WriteStage) appendAcceptedInsert(ref ids.ObjectRef, candidate *indexWriteCandidate) (bool, bool, error) {
	row := candidate.values
	s := w.scope
	if err := s.active(); err != nil {
		return false, false, err
	}
	values := [1][]sqltype.Value{row}
	states := [1]indexWriteState{{indexes: candidate.indexes, precheck: candidate.precheck, exclusion: candidate.exclusion}}
	batch := indexWriteBatch{values: values[:]}
	if candidate.indexes != nil || candidate.precheck != nil || candidate.exclusion != nil {
		batch.states = states[:]
	}
	if err := s.validateIndexCandidates(ref, &batch); err != nil {
		return false, false, err
	}
	*candidate = batch.candidate(0)
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return false, false, errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.catalog.Domain {
		return false, false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.owner.ref.Epoch() {
		return false, false, ids.ErrCatalogReplaced
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	if s.uniqueBinding == nil || s.uniqueBinding.catalog != catalog.State() {
		return false, true, nil
	}
	parent, ok := catalog.Relation(ref.Address)
	if !ok || !sameRelationRowShape(s.uniqueBinding.parent, parent) {
		return false, false, ids.ErrCatalogReplaced
	}
	if e.privateStorageLocked(s.owner.catalog, ref.Address) == nil {
		return false, false, errors.New("stuntgres: relation storage is missing")
	}
	if len(row) != len(parent.Columns) {
		return false, false, errors.New("stuntgres: invalid staged row width")
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return false, false, err
	}
	auth.catalog = catalog
	auth.Recapture(auth.catalog)
	b := s.insertReturning.binding
	if b.viewWrite == nil && s.partitionInsert == nil {
		b.ref, b.relation = ref, parent
	}
	if err = auth.checkBinding(b, &parser.Insert{}); err != nil {
		return false, false, err
	}
	var version storage.RowVersion
	if candidate.exclusion != nil {
		version = *candidate.exclusion
	} else {
		if e.storage.RowFrontier() == ^uint64(0) || e.storage.VersionFrontier() == ^uint64(0) {
			return false, false, errors.New("stuntgres: row or version identity exhausted")
		}
		version = storage.RowVersion{Row: e.storage.AllocateRowLocked(), Version: e.storage.AllocateVersionLocked(), Values: row}
	}
	version.IndexAdmission = catalog.State()
	version.IndexOutputs = candidate.indexes
	if e.visibility.relevantSerializableLocked(s.txRef) != nil {
		events := [1]serializableWrite{{row: version.Row, version: version.Version}}
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
func (s *StatementScope) partitionReturningGroup(ref ids.ObjectRef) *partitionInsertGroup {
	state := s.partitionInsert
	address := state.destination.Address()
	group := state.groups[address]
	if group == nil {
		group = &partitionInsertGroup{ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.groups == nil {
			state.groups = make(map[ids.ObjectAddress]*partitionInsertGroup)
		}
		state.groups[address] = group
		state.order = append(state.order, group)
	}
	s.uniqueBinding = group.unique
	return group
}

// Returning actions own their immutable payload in WriteStage, so callers must
// not retain a duplicate producer prefix. Upsert projects at its action seam.
func (s *StatementScope) admitInsertProducer(b binding, candidate *indexWriteCandidate, prefix indexWriteBatch, c *EvalContext) (bool, error) {
	row := candidate.values
	if s.insertReturning == nil {
		return s.admitInsertRow(b.ref, candidate, prefix, c)
	}
	if s.insertConflict == nil || s.insertConflict.update == nil {
		if err := s.op.session.Engine().checkViewRow(s, &b, row, c); err != nil {
			return false, err
		}
	}
	accepted, err := s.acceptInsertReturning(b.ref, candidate, c)
	if err != nil || !accepted {
		return false, err
	}
	return false, s.projectInsertReturning(row, nil, c)
}
