package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Each proposal is already projected and constrained. Only arbitration is
// restarted after a row wait; defaults, the source and SET are never replayed.
func (s *StatementScope) admitInsertConflictUpdate(ref ids.ObjectRef, candidate *indexWriteCandidate, proposalEvaluation *exec.Context) error {
	proposal := candidate.values
	c := s.insertConflict
	e := s.op.session.Engine()
	manager := e.locks
	manager.BeginUniqueDelta(s.ticket)
	defer manager.EndUniqueDelta(s.ticket)
	// Borrow the producer's one captured query state and canonical parameters.
	// Action columns are an independent target/excluded frame, not source rows.
	evaluation := *proposalEvaluation
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.OldRow = nil
	for {
		if err := evaluation.Context.Err(); err != nil {
			return err
		}
		hit, err := s.probeUniqueRow(ref, candidate, nil, indexWriteBatch{}, c, indexProbeRuntime)
		if err != nil {
			return err
		}
		if hit.index == nil {
			other, err := s.probeUniqueRow(ref, candidate, nil, indexWriteBatch{}, nil, indexProbeRuntime)
			if err != nil {
				return err
			}
			if c.catalog != s.uniqueBinding.catalog {
				manager.RestoreUniqueDelta(s.ticket)
				s.discardExclusionCandidate(ref, candidate)
				continue
			}
			if other.index != nil {
				return other.diagnostic(s, ref)
			}
			if s.viewWrite != nil {
				b := bindingForConflict(ref, s.viewWrite.base, c)
				b.viewWrite = s.viewWrite
				b.targets = c.targets
				if err := e.checkViewRow(s, &b, proposal, &evaluation); err != nil {
					return err
				}
			}
			if s.insertReturning != nil {
				_, retry, err := s.Writes().appendAcceptedInsert(ref, candidate)
				if err != nil {
					return err
				}
				if retry {
					manager.RestoreUniqueDelta(s.ticket)
					s.discardExclusionCandidate(ref, candidate)
					continue
				}
			} else {
				var batch indexWriteBatch
				batch.append(*candidate)
				if err := s.Writes().applyValidated(ref, batch); err != nil {
					return err
				}
			}
			staged := s.stage.rows[ref.Address]
			c.markAffected(staged[len(staged)-1].Row, len(staged)-1)
			return s.projectInsertReturning(proposal, nil, &evaluation)
		}
		// A speculative key must never be held while queuing for the selected row.
		// Restore only this candidate's newly granted keys, preserving prior claims.
		manager.RestoreUniqueDelta(s.ticket)
		s.discardExclusionCandidate(ref, candidate)
		mode := lock.ForNoKeyUpdate
		for _, target := range c.updateTargets {
			for _, key := range s.uniqueBinding.keySlots {
				if target == key {
					mode = lock.ForUpdate
				}
			}
		}
		if err := s.acquireUpdateRowMode(ref, hit.row.Row, mode); err != nil {
			return err
		}
		old, parent, schema, live, err := e.latestConflictUpdateRow(s, ref, hit)
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		// Reacquire row -> key, and demand the same physical identity under both.
		checked, err := s.probeUniqueRow(ref, candidate, nil, indexWriteBatch{}, c, indexProbeRuntime)
		if err != nil {
			return err
		}
		if checked.index == nil || checked.row.Row != old.Row || checked.row.Version != old.Version {
			manager.RestoreUniqueDelta(s.ticket)
			s.discardExclusionCandidate(ref, candidate)
			continue
		}
		if _, duplicate := c.affectedRows[old.Row]; duplicate {
			return conflictUpdateCardinalityError()
		}
		lockMode, err := lock.MultiLockMode(mode)
		if err != nil {
			return err
		}
		if err := s.physicalRowEvent(ref, old, lockMode, 0); err != nil {
			return err
		}
		if c.frame == nil {
			c.frame = make([]sqltype.Value, 2*c.width)
		}
		copy(c.frame, old.Values)
		copy(c.frame[c.width:], proposal)
		evaluation.Row = c.frame
		matches, err := exec.UpdateMatches(&evaluation, c.update.predicate)
		if err != nil {
			return err
		}
		if !matches {
			manager.RestoreUniqueDelta(s.ticket)
			s.discardExclusionCandidate(ref, candidate)
			return nil
		}
		row, err := evaluateUpdateAssignments(&evaluation, c.update, c.updateTargets, old.Values)
		if err != nil {
			return err
		}
		b := bindingForConflict(ref, parent, c)
		// Constraint Detail can disclose SET outputs, not unread carried target data.
		b.targets = c.updateTargets
		if err := e.validatePhysicalInsertRow(s, b, &b, schema, row, &evaluation); err != nil {
			return err
		}
		keyChanged, err := s.uniqueKeyChanged(ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged && mode != lock.ForUpdate {
			if err := s.acquireUpdateRowMode(ref, old.Row, lock.ForUpdate); err != nil {
				return err
			}
			current, _, _, live, err := e.latestConflictUpdateRow(s, ref, checked)
			if err != nil {
				return err
			}
			if !live || current.Version != old.Version {
				return errors.New("stuntgres: conflict update predecessor changed under row lock")
			}
		}
		if err := s.claimUniqueRow(ref, old); err != nil {
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
		replacement := indexWriteCandidate{values: row}
		if err := s.checkUniqueRow(ref, &replacement, []storage.RowVersion{old}, indexWriteBatch{}); err != nil {
			return err
		}
		b.viewWrite = s.viewWrite
		if err := e.checkViewRow(s, &b, row, &evaluation); err != nil {
			return err
		}
		var batch indexWriteBatch
		batch.actionMode = actionMode
		batch.append(replacement)
		if err := s.Writes().replaceDistinct(ref, []storage.RowVersion{old}, batch); err != nil {
			return err
		}
		c.markAffected(old.Row, len(s.stage.rows[ref.Address])-1)
		return s.projectInsertReturning(row, old.Values, &evaluation)
	}
}

func (c *boundInsertConflict) markAffected(id ids.RowID, slot int) {
	if c.affectedRows == nil {
		c.affectedRows = make(map[ids.RowID]int)
	}
	c.affectedRows[id] = slot
	c.affected++
}
func conflictUpdateCardinalityError() error {
	d := sqlError("21000", "ON CONFLICT DO UPDATE command cannot affect row a second time").(*sqlstate.Error)
	d.Hint = "Ensure that no rows proposed for insertion within the same command have duplicate constrained values."
	return d
}

// Committed hits retain their provider's exact physical hint. Private overlays
// are keyed by RowID, with this command's replacement ahead of prior writes.
func (e *Engine) latestConflictUpdateRow(s *StatementScope, ref ids.ObjectRef, candidate uniqueConflict) (storage.RowVersion, *catalog.Relation, string, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, nil, "", false, err
	}
	if ref.Epoch != e.catalog.state.Epoch() {
		return storage.RowVersion{}, nil, "", false, ids.ErrCatalogReplaced
	}
	catalog := e.privateCatalogLocked(s.owner.catalog)
	parent, ok := catalog.Relation(ref.Address)
	store := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if !ok || store == nil || !sameRelationRowShape(candidate.parent, parent) {
		return storage.RowVersion{}, nil, "", false, ids.ErrCatalogReplaced
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return storage.RowVersion{}, nil, "", false, err
	}
	auth.Catalog = catalog
	auth.Recapture(auth.Catalog)
	if err := auth.checkBinding(bindingForConflict(ref, parent, s.insertConflict), &parser.Insert{}); err != nil {
		return storage.RowVersion{}, nil, "", false, err
	}
	namespace, ok := catalog.Namespace(parent.Namespace())
	if !ok {
		return storage.RowVersion{}, nil, "", false, ids.ErrCatalogReplaced
	}
	var row storage.RowVersion
	var found bool
	if candidate.committed {
		row, _, found = rowByID(store.Versions, candidate.row.Row, candidate.position)
	} else {
		if slot, ok := s.insertConflict.affectedRows[candidate.row.Row]; ok {
			row, found = s.stage.rows[ref.Address][slot], true
		}
		if !found {
			row, found = s.insertOwnRow(ref, candidate.row.Row)
		}
	}
	if !found {
		return storage.RowVersion{}, nil, "", false, errors.New("stuntgres: invalid conflict update predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return storage.RowVersion{}, nil, "", false, err
	}
	live := visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address]) && !uniqueRowDeleted(row.Row, s.stage.deletes[ref.Address])
	return row, parent, namespace.Name(), live, nil
}
