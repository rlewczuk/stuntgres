package engine

import (
	"errors"
	"stuntgres/internal/engine/dml"
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
func (s *StatementScope) admitInsertConflictUpdate(ref ids.ObjectRef, candidate *index.WriteCandidate, proposalEvaluation *exec.Context) error {
	proposal := candidate.Values
	c := s.InsertConflict
	e := s.engine()
	journal := s.uniqueJournal()
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
		hit, err := dml.ProbeUniqueRow(s.writeHost(), ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if hit.Index == nil {
			other, err := dml.ProbeUniqueRow(s.writeHost(), ref, candidate, nil, index.WriteBatch{}, nil, index.ProbeRuntime)
			if err != nil {
				return err
			}
			if c.Catalog != s.UniqueBinding.Catalog {
				journal.RestoreDelta()
				s.writeHost().DiscardExclusion(candidate)
				continue
			}
			if other.Index != nil {
				return other.Diagnostic(s.writeHost(), ref)
			}
			if s.ViewWrite != nil {
				b := dml.BindingForConflict(ref, s.ViewWrite.BaseRelation, c)
				b.ViewWrite = view.ViewWriteSlotOf(s.ViewWrite)
				b.Targets = c.Targets
				if err := e.checkViewRow(s, &b, proposal, &evaluation); err != nil {
					return err
				}
			}
			if s.InsertReturning != nil {
				_, retry, err := s.Writes().appendAcceptedInsert(ref, candidate)
				if err != nil {
					return err
				}
				if retry {
					journal.RestoreDelta()
					s.writeHost().DiscardExclusion(candidate)
					continue
				}
			} else {
				var batch index.WriteBatch
				batch.Append(*candidate)
				if err := dml.ApplyValidated(s.writeHost(), ref, batch); err != nil {
					return err
				}
			}
			staged := s.stage.rows[ref.Address]
			c.MarkAffected(staged[len(staged)-1].Row, len(staged)-1)
			return s.projectInsertReturning(proposal, nil, &evaluation)
		}
		// A speculative key must never be held while queuing for the selected row.
		// Restore only this candidate's newly granted keys, preserving prior claims.
		journal.RestoreDelta()
		s.writeHost().DiscardExclusion(candidate)
		mode := lock.ForNoKeyUpdate
		for _, target := range c.UpdateTargets {
			for _, key := range s.UniqueBinding.KeySlots {
				if target == key {
					mode = lock.ForUpdate
				}
			}
		}
		if err := s.acquireUpdateRowMode(ref, hit.Row.Row, mode); err != nil {
			return err
		}
		latest, err := s.writeHost().LatestRow(conflictRowRequest(ref, hit))
		old, parent, schema, live := latest.Version, latest.Parent, latest.Schema, latest.Live
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		// Reacquire row -> key, and demand the same physical identity under both.
		checked, err := dml.ProbeUniqueRow(s.writeHost(), ref, candidate, nil, index.WriteBatch{}, c, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if checked.Index == nil || checked.Row.Row != old.Row || checked.Row.Version != old.Version {
			journal.RestoreDelta()
			s.writeHost().DiscardExclusion(candidate)
			continue
		}
		if _, duplicate := c.AffectedRows[old.Row]; duplicate {
			return dml.ConflictUpdateCardinalityError()
		}
		lockMode, err := lock.MultiLockMode(mode)
		if err != nil {
			return err
		}
		if err := s.physicalRowEvent(ref, old, lockMode, 0); err != nil {
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
			s.writeHost().DiscardExclusion(candidate)
			return nil
		}
		row, err := evaluateUpdateAssignments(&evaluation, c.Update, c.UpdateTargets, old.Values)
		if err != nil {
			return err
		}
		b := dml.BindingForConflict(ref, parent, c)
		// Constraint Detail can disclose SET outputs, not unread carried target data.
		b.Targets = c.UpdateTargets
		if err := e.validatePhysicalInsertRow(s, b, &b, schema, row, &evaluation); err != nil {
			return err
		}
		keyChanged, err := dml.UniqueKeyChanged(s.writeHost(), ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged && mode != lock.ForUpdate {
			if err := s.acquireUpdateRowMode(ref, old.Row, lock.ForUpdate); err != nil {
				return err
			}
			latest, err := s.writeHost().LatestRow(conflictRowRequest(ref, checked))
			current, live := latest.Version, latest.Live
			if err != nil {
				return err
			}
			if !live || current.Version != old.Version {
				return errors.New("stuntgres: conflict update predecessor changed under row lock")
			}
		}
		if err := dml.ClaimUniqueRow(s.writeHost(), ref, old); err != nil {
			return err
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := s.physicalRowEvent(ref, old, actionMode, 0); err != nil {
			return err
		}
		replacement := index.WriteCandidate{Values: row}
		if err := dml.CheckUniqueRow(s.writeHost(), ref, &replacement, []storage.RowVersion{old}, index.WriteBatch{}); err != nil {
			return err
		}
		b.ViewWrite = view.ViewWriteSlotOf(s.ViewWrite)
		if err := e.checkViewRow(s, &b, row, &evaluation); err != nil {
			return err
		}
		var batch index.WriteBatch
		batch.ActionMode = actionMode
		batch.Append(replacement)
		if err := dml.ReplaceRows(s.writeHost(), ref, []storage.RowVersion{old}, batch, dml.ReplaceConflict); err != nil {
			return err
		}
		c.MarkAffected(old.Row, len(s.stage.rows[ref.Address])-1)
		return s.projectInsertReturning(row, old.Values, &evaluation)
	}
}

func preflightConflictUpdate(c *dml.InsertConflictState, evaluation *exec.Context) error {
	if c.Update == nil {
		return nil
	}
	action, err := preflightUpdateWithPlanning(evaluation, c.Update, true)
	if err != nil {
		return err
	}
	c.Update = action
	return nil
}

// conflictRowRequest is the RowConflictUpdate request of a unique conflict.
func conflictRowRequest(ref ids.ObjectRef, c dml.UniqueConflict) dml.RowRequest {
	return dml.RowRequest{Kind: dml.RowConflictUpdate, Ref: ref, Row: c.Row.Row, Own: !c.Committed, Hint: c.Position, Parent: c.Parent}
}
