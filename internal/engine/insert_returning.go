package engine

import (
	"errors"
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
func (s *StatementScope) acceptInsertReturning(ref ObjectRef, row []sqltype.Value, evaluation *EvalContext) (bool, error) {
	if s.insertConflict != nil && s.insertConflict.update != nil {
		return false, s.admitInsertConflictUpdate(ref, row, evaluation)
	}
	if s.partitionInsert != nil {
		group := s.partitionReturningGroup(ref)
		ref = group.ref
		defer func() { group.unique = s.uniqueBinding }()
	}
	for {
		admitted := true
		var err error
		if s.partitionInsert != nil {
			err = s.checkUniqueRow(ref, row, nil, nil)
		} else {
			admitted, err = s.admitInsertRow(ref, row, nil, evaluation)
		}
		if err != nil || !admitted {
			return false, err
		}
		accepted, retry, err := s.Writes().appendAcceptedInsert(ref, row)
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
func (w *WriteStage) appendAcceptedInsert(ref ObjectRef, row []sqltype.Value) (bool, bool, error) {
	s := w.scope
	if err := s.active(); err != nil {
		return false, false, err
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return false, false, errors.New("stuntgres: inactive write stage")
	}
	if ref.owner != e.owner {
		return false, false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.owner.ref.epoch {
		return false, false, ErrCatalogReplaced
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	if s.uniqueBinding == nil || s.uniqueBinding.catalog != catalog.state {
		return false, true, nil
	}
	parent, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || !sameRelationRowShape(s.uniqueBinding.parent, parent) {
		return false, false, ErrCatalogReplaced
	}
	if e.privateStorageLocked(s.owner.catalog, ref.Address) == nil {
		return false, false, errors.New("stuntgres: relation storage is missing")
	}
	if len(row) != len(parent.columns) {
		return false, false, errors.New("stuntgres: invalid staged row width")
	}
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return false, false, err
	}
	auth.catalog = catalog
	auth.roles.catalog = auth.catalog
	b := s.insertReturning.binding
	if b.viewWrite == nil && s.partitionInsert == nil {
		b.ref, b.relation = ref, parent
	}
	if err = auth.checkBinding(b, &parser.Insert{}); err != nil {
		return false, false, err
	}
	version, found := s.reservedExclusionRow(ref, row)
	if !found {
		if e.nextRow == ^uint64(0) || e.nextVersion == ^uint64(0) {
			return false, false, errors.New("stuntgres: row or version identity exhausted")
		}
		e.nextRow++
		e.nextVersion++
		version = rowVersion{row: RowID(e.nextRow), version: VersionID(e.nextVersion), values: row}
	}
	version.indexAdmission = catalog.state
	if serializableRelevant(e.serializable.nodes[s.txRef]) {
		events := [1]serializableWrite{{row: version.row, version: version.version}}
		if err = e.serializableWriteLocked(s, ref, events[:]); err != nil {
			return false, false, err
		}
	}
	if w.rows == nil {
		w.rows = make(map[ObjectAddress][]rowVersion)
	}
	w.rows[ref.Address] = append(w.rows[ref.Address], version)
	return true, false, nil
}
func (s *StatementScope) partitionReturningGroup(ref ObjectRef) *partitionInsertGroup {
	state := s.partitionInsert
	address := state.destination.address
	group := state.groups[address]
	if group == nil {
		group = &partitionInsertGroup{ref: ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: address}}
		if state.groups == nil {
			state.groups = make(map[ObjectAddress]*partitionInsertGroup)
		}
		state.groups[address] = group
		state.order = append(state.order, group)
	}
	s.uniqueBinding = group.unique
	return group
}

// Returning actions own their immutable payload in WriteStage, so callers must
// not retain a duplicate producer prefix. Upsert projects at its action seam.
func (s *StatementScope) admitInsertProducer(b binding, row []sqltype.Value, prefix [][]sqltype.Value, c *EvalContext) (bool, error) {
	if s.insertReturning == nil {
		return s.admitInsertRow(b.ref, row, prefix, c)
	}
	if s.insertConflict == nil || s.insertConflict.update == nil {
		if err := s.op.session.Engine().checkViewRow(s, &b, row, c); err != nil {
			return false, err
		}
	}
	accepted, err := s.acceptInsertReturning(b.ref, row, c)
	if err != nil || !accepted {
		return false, err
	}
	return false, s.projectInsertReturning(row, nil, c)
}
