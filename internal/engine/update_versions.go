package engine

import (
	"errors"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// applyValidated stages a validated insert batch: SQL producers validate
// each accepted prefix before demanding the next row, and only this loop
// transfers the completed immutable batch, through the StageInsert window.
// After a stale unique binding it revalidates the batch and retries.
func applyValidated(h dml.Host, ref ids.ObjectRef, batch index.WriteBatch) error {
	if err := h.Active(); err != nil {
		return err
	}
	for {
		if err := validateIndexCandidates(h, ref, &batch); err != nil {
			return err
		}
		stale, err := h.StageInsert(ref, batch)
		if err != nil || !stale {
			return err
		}
		if c := h.Command(); c.InsertConflict != nil && c.InsertConflict.Update == nil {
			batch, err = revalidateConflictRows(h, ref, batch)
			if err != nil {
				return err
			}
		} else {
			if err := revalidateIndexRows(h, ref, nil, &batch); err != nil {
				return err
			}
		}
	}
}

// replaceRows stages the successors of old in lane: it classifies key
// changes, takes the row grants and validates the index candidates around
// the StageReplacement window, revalidating and retrying after a stale
// unique binding. ReplaceAppend is for ordinary UPDATE, which proves each
// accepted physical target distinct through its frozen scan / FROM winner
// gate and excludes existing staged RowIDs; ReplaceConflict is for ON
// CONFLICT DO UPDATE, which proves cardinality through its affected-row set.
func replaceRows(h dml.Host, ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, lane dml.ReplaceLane) error {
	rows := batch.Values
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
			changed, failure := uniqueKeyChanged(h, ref, predecessor.Values, rows[i])
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
		if err := h.AcquireUpdateRow(ref, predecessor.Row, grant); err != nil {
			return err
		}
	}
	for {
		if err := validateIndexCandidates(h, ref, &batch); err != nil {
			return err
		}
		stale, err := h.StageReplacement(ref, old, batch, modes, lane)
		if err != nil || !stale {
			return err
		}
		if err := revalidateIndexRows(h, ref, old, &batch); err != nil {
			return err
		}
	}
}
