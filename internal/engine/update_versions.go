package engine

import (
	"errors"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// Conflict UPDATE separately proves cardinality through its affected-row set.
func (w *WriteStage) replaceDistinct(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, dml.ReplaceConflict)
}

func (w *WriteStage) replace(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, dml.ReplaceMerge)
}

// Ordinary UPDATE proves each accepted physical target distinct through its
// frozen scan / FROM winner gate, and excludes existing staged RowIDs before
// choosing this append lane. Ordinary target and RI validation still applies.
func (w *WriteStage) appendUpdateReplacement(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, dml.ReplaceAppend)
}

// replaceRows classifies key changes, takes the row grants and validates the
// index candidates around the StageReplacement window, revalidating and
// retrying after a stale unique binding.
func (w *WriteStage) replaceRows(ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, lane dml.ReplaceLane) error {
	rows := batch.Values
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	h := s.writeHost()
	if err := h.PrepareReplacement(ref, old); err != nil {
		return err
	}
	if len(old) != len(rows) {
		return errors.New("stuntgres: invalid replacement shape")
	}
	var smallModes [1]lock.MultiMode
	var modes []lock.MultiMode
	if batch.ActionMode == 0 {
		modes = smallModes[:min(len(old), 1)]
		if len(old) > 1 {
			modes = make([]lock.MultiMode, len(old))
		}
	}
	for i, predecessor := range old {
		mode := batch.ActionMode
		if mode == 0 {
			changed, failure := s.uniqueKeyChanged(ref, predecessor.Values, rows[i])
			if failure != nil {
				return failure
			}
			mode = lock.MultiNoKeyAction
			if changed {
				mode = lock.MultiUpdateAction
			}
			modes[i] = mode
		}
		if mode != lock.MultiNoKeyAction && mode != lock.MultiUpdateAction {
			return errors.New("stuntgres: invalid replacement action intent")
		}
		grant := lock.ForNoKeyUpdate
		if mode == lock.MultiUpdateAction {
			grant = lock.ForUpdate
		}
		if err := s.acquireUpdateRowMode(ref, predecessor.Row, grant); err != nil {
			return err
		}
	}
	for {
		if err := s.validateIndexCandidates(ref, &batch); err != nil {
			return err
		}
		stale, err := h.StageReplacement(ref, old, batch, modes, lane)
		if err != nil || !stale {
			return err
		}
		if err := s.revalidateIndexRows(ref, old, &batch); err != nil {
			return err
		}
	}
}
