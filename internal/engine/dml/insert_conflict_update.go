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
	var accepted []storage.RowVersion
	if command.InsertReturning != nil && returningHasPhysicalHeaders(command.InsertReturning.Binding.Returning) {
		accepted = make([]storage.RowVersion, 1)
	}
	for {
		if err := evaluation.Context.Err(); err != nil {
			return err
		}
		hit, err := ProbeUniqueRow(evaluation.Context, h, ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if hit.Index == nil {
			other, err := ProbeUniqueRow(evaluation.Context, h, ref, candidate, nil, index.WriteBatch{}, nil, index.ProbeRuntime)
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
				_, retry, physical, err := AppendAcceptedInsert(h, ref, candidate)
				if err != nil {
					return err
				}
				if retry {
					journal.RestoreDelta()
					DiscardExclusion(h, candidate)
					continue
				}
				evaluation.PhysicalTable, evaluation.PhysicalVersion = ref, physical
			} else {
				var batch index.WriteBatch
				batch.Append(*candidate)
				if err := ApplyValidated(evaluation.Context, h, ref, batch); err != nil {
					return err
				}
			}
			staged := h.PrivateRows(ref.Address).Staged
			c.MarkAffected(staged[len(staged)-1], len(staged)-1)
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
		if err := h.AcquireUpdateRow(ref, hit.Row, mode); err != nil {
			return err
		}
		latest, err := h.LatestRow(conflictRowRequest(ref, hit, mode))
		old, parent, schema, live := latest.Version, latest.Parent, latest.Schema, latest.Live
		hint := latest.Hint
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		// Reacquire row -> key, and demand the same physical identity under both.
		checked, err := ProbeUniqueRow(evaluation.Context, h, ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if checked.Index == nil || checked.Row.Row != old.Row || checked.Row.Version != old.Version {
			journal.RestoreDelta()
			DiscardExclusion(h, candidate)
			continue
		}
		if _, duplicate := c.AffectedSlot(old); duplicate {
			return ConflictUpdateCardinalityError()
		}
		prologueDone := false
	retryConflictUpdate:
		for {
			if prologueDone {
				latest, err := h.LatestRow(conflictRowRequest(ref, checked, mode))
				if err != nil {
					return err
				}
				if !latest.Live {
					return nil
				}
				old, parent, schema = latest.Version, latest.Parent, latest.Schema
				hint = latest.Hint
			}
			if err := h.PhysicalRowLock(ref, old, mode); err != nil {
				return err
			}
			if c.Frame == nil {
				c.Frame = make([]sqltype.Value, 2*c.Width)
			}
			copy(c.Frame, old.Values)
			copy(c.Frame[c.Width:], proposal)
			evaluation.Row = c.Frame
			evaluation.PhysicalTable, evaluation.PhysicalVersion = ref, old
			evaluation.PhysicalXmaxValid, evaluation.OldPhysicalXmaxValid = false, false
			matches, err := exec.UpdateMatches(&evaluation, c.Update.Predicate)
			if err != nil {
				return err
			}
			if !matches {
				if !prologueDone {
					journal.RestoreDelta()
				}
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
			if !prologueDone {
				if parent.HasTriggers() {
					targets := UpdateTargetAttributes(h, ref, parent, c.UpdateTargets, nil)
					before, failure := Before(h, parent, BeforeRow{Relation: ref, Event: BeforeUpdate, Old: old.Values, New: row, Targets: targets})
					if failure != nil {
						return failure
					}
					if !before.Proceed {
						journal.RestoreDelta()
						DiscardExclusion(h, candidate)
						return nil
					}
					row = before.Row
				}
				prologueDone = true
			}
			if err := validatePhysicalInsertRow(h, b, &b, schema, row, &evaluation); err != nil {
				return err
			}
			keyChanged, err := UniqueKeyChanged(h, ref, old.Values, row)
			if err != nil {
				return err
			}
			if keyChanged && mode != lock.ForUpdate {
				if err := h.AcquireUpdateRow(ref, old, lock.ForUpdate); err != nil {
					return err
				}
				latest, err := h.LatestRow(conflictRowRequest(ref, checked, lock.ForUpdate))
				current, live := latest.Version, latest.Live
				if err != nil {
					return err
				}
				if !live {
					return nil
				}
				if current.Version != old.Version {
					continue retryConflictUpdate
				}
				hint = latest.Hint
			}
			if err := ClaimUniqueRow(evaluation.Context, h, ref, old); err != nil {
				return err
			}
			old.PendingKeyChange = old.PendingKeyChange || keyChanged
			actionMode := lock.MultiNoKeyAction
			if keyChanged {
				actionMode = lock.MultiUpdateAction
			}
			replacement := index.WriteCandidate{Values: row}
			if err := CheckUniqueRow(evaluation.Context, h, ref, &replacement, []storage.RowVersion{old}, index.WriteBatch{}); err != nil {
				return err
			}
			b.ViewWrite = view.ViewWriteSlotOf(command.ViewWrite)
			if err := CheckViewRow(h, &b, row, &evaluation); err != nil {
				return err
			}
			var batch index.WriteBatch
			batch.ActionMode = actionMode
			batch.PredecessorHint, batch.PredecessorHintKnown = hint, hint >= 0
			batch.Append(replacement)
			batch.Accepted = accepted
			if _, err := ReplaceRows(evaluation.Context, h, ref, []storage.RowVersion{old}, batch, ReplaceConflict); err != nil {
				if errors.Is(err, ErrTupleRecheck) {
					DiscardExclusion(h, &replacement)
					continue retryConflictUpdate
				}
				return err
			}
			if accepted != nil {
				evaluation.PhysicalTable, evaluation.PhysicalVersion = ref, accepted[0]
			}
			evaluation.OldPhysicalTable, evaluation.OldPhysicalVersion = ref, old
			if accepted != nil {
				if accepted[0].PrivateHistory != nil {
					evaluation.OldPhysicalVersion = accepted[0].PrivateHistory.Version
				}
				evaluation.OldPhysicalVersion.DeleteFullXID = accepted[0].InsertFullXID
			}
			staged := h.PrivateRows(ref.Address).Staged
			slot := len(staged) - 1
			c.MarkAffected(staged[slot], slot)
			c.AffectedVersions[writeIdentity(old)] = slot
			return projectInsertReturning(h, row, old.Values, &evaluation)
		}
	}
}

// conflictRowRequest is the RowConflictUpdate request of a unique conflict.
func conflictRowRequest(ref ids.ObjectRef, c UniqueConflict, mode lock.Mode) RowRequest {
	return RowRequest{Kind: RowConflictUpdate, Ref: ref, Row: c.Row.Row, Physical: c.Row.Version, Own: !c.Committed, Hint: c.Position, Parent: c.Parent, GrantMode: mode}
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
