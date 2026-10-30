package dml

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// revalidateIndexRows rebinds after a stale unique binding, readmits the
// statement's programs and reprobes every row of batch.
func revalidateIndexRows(ctx context.Context, h Host, ref ids.ObjectRef, old []storage.RowVersion, batch *index.WriteBatch) error {
	c := h.Command()
	prior := c.UniqueBinding
	if prior != nil && prior.Ref != ref {
		prior = c.IndexBindings[ref.Address]
	}
	binding, err := BindUniqueWrite(h, ref)
	if err != nil {
		return err
	}
	purpose := index.ProbeRuntime
	if binding.Checked || prior != nil && prior.Checked {
		purpose = index.ProbeRevalidation
		if prior != nil && !index.SameRelationRowShape(prior.Parent, binding.Parent) {
			return ids.ErrCatalogReplaced
		}
	}
	if prior != nil {
		if err := h.ReadmitRevalidation(prior, binding); err != nil {
			return err
		}
	}
	for i := range batch.Values {
		candidate := batch.Candidate(i)
		var excluded []storage.RowVersion
		if old != nil {
			excluded = old[:i+1]
		}
		conflict, err := ProbeUniqueRow(ctx, h, ref, &candidate, excluded, batch.Prefix(i), nil, purpose)
		if err != nil {
			return err
		}
		if conflict.Index != nil {
			return conflict.Diagnostic(h, ref)
		}
		batch.Set(i, candidate)
	}
	return nil
}

// ApplyValidated stages a validated insert batch: SQL producers validate
// each accepted prefix before demanding the next row, and only this loop
// transfers the completed immutable batch, through the StageInsert window.
// After a stale unique binding it revalidates the batch and retries.
func ApplyValidated(ctx context.Context, h Host, ref ids.ObjectRef, batch index.WriteBatch) error {
	if err := h.Active(); err != nil {
		return err
	}
	for {
		if err := ValidateIndexCandidates(h, ref, &batch); err != nil {
			return err
		}
		stale, err := h.StageInsert(ref, batch)
		if err != nil || !stale {
			return err
		}
		if c := h.Command(); c.InsertConflict != nil && c.InsertConflict.Update == nil {
			batch, err = revalidateConflictRows(ctx, h, ref, batch)
			if err != nil {
				return err
			}
		} else {
			if err := revalidateIndexRows(ctx, h, ref, nil, &batch); err != nil {
				return err
			}
		}
	}
}

// ReplaceRows stages the successors of old in lane: it classifies key
// changes, takes the row grants and validates the index candidates around
// the StageReplacement window, revalidating and retrying after a stale
// unique binding. ReplaceAppend is for ordinary UPDATE, which proves each
// accepted physical target distinct through its frozen scan / FROM winner
// gate and excludes existing staged RowIDs; ReplaceConflict is for ON
// CONFLICT DO UPDATE, which proves cardinality through its affected-row set.
func ReplaceRows(ctx context.Context, h Host, ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, lane ReplaceLane) error {
	rows := batch.Values
	if err := h.PrepareReplacement(ref, old); err != nil {
		return err
	}
	if len(old) != len(rows) {
		return errors.New("stuntgres: invalid replacement shape")
	}
	// Production callers always give an ActionMode; only the per-row
	// fallback allocates, because modes escapes through the host.
	var modes []lock.MultiMode
	if batch.ActionMode == 0 {
		modes = make([]lock.MultiMode, len(old))
	}
	for i, predecessor := range old {
		mode := batch.ActionMode
		if mode == 0 {
			changed, failure := UniqueKeyChanged(h, ref, predecessor.Values, rows[i])
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
		if err := ValidateIndexCandidates(h, ref, &batch); err != nil {
			return err
		}
		stale, err := h.StageReplacement(ref, old, batch, modes, lane)
		if err != nil || !stale {
			return err
		}
		if err := revalidateIndexRows(ctx, h, ref, old, &batch); err != nil {
			return err
		}
	}
}
