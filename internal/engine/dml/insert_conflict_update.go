package dml

import (
	"errors"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// Each proposal is already projected and constrained. Only arbitration is
// restarted after a row wait; defaults, the source and SET are never replayed.
func admitInsertConflictUpdate(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, proposalEvaluation *exec.Context) error {
	proposal := candidate.Values
	command := h.Command()
	c := command.InsertConflict
	journal := h.UniqueJournal()
	journal.BeginDelta()
	defer journal.EndDelta()
	// Borrow the producer's one captured query state and canonical parameters.
	// Action columns are an independent target/excluded frame, not source rows.
	evaluation := *proposalEvaluation
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.OldRow = nil
	for {
		if err := evaluation.Context.Err(); err != nil {
			return err
		}
		hit, err := ProbeUniqueRow(h, ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if hit.Index == nil {
			other, err := ProbeUniqueRow(h, ref, candidate, nil, index.WriteBatch{}, nil, index.ProbeRuntime)
			if err != nil {
				return err
			}
			if c.Catalog != command.UniqueBinding.Catalog {
				journal.RestoreDelta()
				DiscardExclusion(h, candidate)
				continue
			}
			if other.Index != nil {
				return other.Diagnostic(h, ref)
			}
			if command.ViewWrite != nil {
				b := BindingForConflict(ref, command.ViewWrite.BaseRelation, c)
				b.ViewWrite = view.ViewWriteSlotOf(command.ViewWrite)
				b.Targets = c.Targets
				if err := CheckViewRow(h, &b, proposal, &evaluation); err != nil {
					return err
				}
			}
			if command.InsertReturning != nil {
				_, retry, err := AppendAcceptedInsert(h, ref, candidate)
				if err != nil {
					return err
				}
				if retry {
					journal.RestoreDelta()
					DiscardExclusion(h, candidate)
					continue
				}
			} else {
				var batch index.WriteBatch
				batch.Append(*candidate)
				if err := ApplyValidated(h, ref, batch); err != nil {
					return err
				}
			}
			staged := h.PrivateRows(ref.Address).Staged
			c.MarkAffected(staged[len(staged)-1].Row, len(staged)-1)
			return projectInsertReturning(h, proposal, nil, &evaluation)
		}
		// A speculative key must never be held while queuing for the selected row.
		// Restore only this candidate's newly granted keys, preserving prior claims.
		journal.RestoreDelta()
		DiscardExclusion(h, candidate)
		mode := lock.ForNoKeyUpdate
		for _, target := range c.UpdateTargets {
			for _, key := range command.UniqueBinding.KeySlots {
				if target == key {
					mode = lock.ForUpdate
				}
			}
		}
		if err := h.AcquireUpdateRow(ref, hit.Row.Row, mode); err != nil {
			return err
		}
		latest, err := h.LatestRow(conflictRowRequest(ref, hit))
		old, parent, schema, live := latest.Version, latest.Parent, latest.Schema, latest.Live
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		// Reacquire row -> key, and demand the same physical identity under both.
		checked, err := ProbeUniqueRow(h, ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if checked.Index == nil || checked.Row.Row != old.Row || checked.Row.Version != old.Version {
			journal.RestoreDelta()
			DiscardExclusion(h, candidate)
			continue
		}
		if _, duplicate := c.AffectedRows[old.Row]; duplicate {
			return ConflictUpdateCardinalityError()
		}
		lockMode, err := lock.MultiLockMode(mode)
		if err != nil {
			return err
		}
		if err := h.PhysicalRowEvent(ref, old, lockMode, 0); err != nil {
			return err
		}
		if c.Frame == nil {
			c.Frame = make([]sqltype.Value, 2*c.Width)
		}
		copy(c.Frame, old.Values)
		copy(c.Frame[c.Width:], proposal)
		evaluation.Row = c.Frame
		matches, err := exec.UpdateMatches(&evaluation, c.Update.Predicate)
		if err != nil {
			return err
		}
		if !matches {
			journal.RestoreDelta()
			DiscardExclusion(h, candidate)
			return nil
		}
		row, err := EvaluateUpdateAssignments(&evaluation, c.Update, c.UpdateTargets, old.Values)
		if err != nil {
			return err
		}
		b := BindingForConflict(ref, parent, c)
		// Constraint Detail can disclose SET outputs, not unread carried target data.
		b.Targets = c.UpdateTargets
		if err := validatePhysicalInsertRow(h, b, &b, schema, row, &evaluation); err != nil {
			return err
		}
		keyChanged, err := UniqueKeyChanged(h, ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged && mode != lock.ForUpdate {
			if err := h.AcquireUpdateRow(ref, old.Row, lock.ForUpdate); err != nil {
				return err
			}
			latest, err := h.LatestRow(conflictRowRequest(ref, checked))
			current, live := latest.Version, latest.Live
			if err != nil {
				return err
			}
			if !live || current.Version != old.Version {
				return errors.New("stuntgres: conflict update predecessor changed under row lock")
			}
		}
		if err := ClaimUniqueRow(h, ref, old); err != nil {
			return err
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := h.PhysicalRowEvent(ref, old, actionMode, 0); err != nil {
			return err
		}
		replacement := index.WriteCandidate{Values: row}
		if err := CheckUniqueRow(h, ref, &replacement, []storage.RowVersion{old}, index.WriteBatch{}); err != nil {
			return err
		}
		b.ViewWrite = view.ViewWriteSlotOf(command.ViewWrite)
		if err := CheckViewRow(h, &b, row, &evaluation); err != nil {
			return err
		}
		var batch index.WriteBatch
		batch.ActionMode = actionMode
		batch.Append(replacement)
		if err := ReplaceRows(h, ref, []storage.RowVersion{old}, batch, ReplaceConflict); err != nil {
			return err
		}
		c.MarkAffected(old.Row, len(h.PrivateRows(ref.Address).Staged)-1)
		return projectInsertReturning(h, row, old.Values, &evaluation)
	}
}

// conflictRowRequest is the RowConflictUpdate request of a unique conflict.
func conflictRowRequest(ref ids.ObjectRef, c UniqueConflict) RowRequest {
	return RowRequest{Kind: RowConflictUpdate, Ref: ref, Row: c.Row.Row, Own: !c.Committed, Hint: c.Position, Parent: c.Parent}
}

// PreflightConflictUpdate preflights the ON CONFLICT DO UPDATE action of the
// private conflict state.
func PreflightConflictUpdate(c *InsertConflictState, evaluation *exec.Context) error {
	if c.Update == nil {
		return nil
	}
	action, err := PreflightUpdateWithPlanning(evaluation, c.Update, true)
	if err != nil {
		return err
	}
	c.Update = action
	return nil
}
