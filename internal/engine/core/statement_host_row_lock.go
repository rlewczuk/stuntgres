package core

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// The source manifest, not target-only DML authority or a re-resolved name,
// identifies each grant. Waiting shares the existing journal/deadline machinery.
// It may block in the lock manager: the replacement read lock is released
// across the wait and retaken for implicit statements, then Engine.mu is taken.
func (h *statementHost) AcquireQueryRow(leaf exec.Leaf, row ids.RowID, mark plan.RowMark) (bool, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return false, err
	}
	if s.LockTicket == nil || s.LockTicket.Ref() != s.TxRef || session.TxOwnerOf(s.TxRef) != s.Owner || s.TxRef.Session() != s.Op.Session.SessionToken || row == 0 || leaf.Storage == nil {
		return false, fmt.Errorf("stuntgres: invalid query row acquisition authority")
	}
	e := s.Engine()
	// Conditional requests reuse the manager's atomic immediate-grant branch:
	// no journal request or timer exists on an unavailable NOWAIT/SKIP probe.
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, NoWait: mark.Wait != parser.RowLockBlock, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: leaf.Ref, Row: row}, mark.Mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		if mark.Wait == parser.RowLockSkip {
			return false, nil
		}
		return false, SQLError("55P03", fmt.Sprintf("could not obtain lock on row in relation %q", leaf.Relation.Name()))
	}
	if err != nil {
		return false, err
	}
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		if err = request.Wait(); err != nil {
			return false, err
		}
		if s.implicit {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	_, err = CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.PendingCatalog()), s.PendingCatalog(), leaf)
	if err == nil {
		err = e.AssignTransactionXIDLocked(s.TxRef)
	}
	return err == nil, err
}

// LatestQueryRow takes Engine.mu for the current version and records the
// physical row event after releasing it.
func (h *statementHost) LatestQueryRow(leaf exec.Leaf, candidate storage.RowVersion, hint int, mode lock.Mode) (result storage.RowVersion, present bool, err error) {
	s := h.scope()
	e := s.Engine()
	var physical storage.RowVersion
	defer func() {
		if err != nil || !present {
			return
		}
		kind, failure := lock.MultiLockMode(mode)
		if failure != nil {
			err = failure
			return
		}
		err = s.physicalRowEvent(leaf.Ref, physical, kind, 0)
	}()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if _, err := CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.PendingCatalog()), s.PendingCatalog(), leaf); err != nil {
		return storage.RowVersion{}, false, err
	}
	store := leaf.Storage
	rows := store.Versions
	if candidate.Born == 0 {
		rows = s.OwnWrites[leaf.Ref.Address]
	}
	current, _, found := storage.RowByID(rows, candidate.Row, hint)
	if !found {
		return storage.RowVersion{}, false, fmt.Errorf("stuntgres: invalid query row predecessor")
	}
	physical = current
	present = visibility.LiveVersion(current, e.Visibility.CommitSeq(), store.Ends, s.OwnDeletes[leaf.Ref.Address])
	if s.Owner.Isolation.Stable() && current.Born != 0 {
		changed := current.Born > s.Owner.Frontier
		if store.Ends != nil {
			end, ok := store.Ends.Rows[current.Row]
			changed = changed || ok && end.Version == current.Version && end.End > s.Owner.Frontier
		}
		if changed {
			// KEY SHARE retains RR values only across compatible replacements.
			// A key transition after the frontier remains incompatible even if
			// later updates restore the key or change only payload.
			if mode == lock.ForKeyShare && present && current.Version != candidate.Version && current.KeyChangedAt <= s.Owner.Frontier {
				return candidate, true, nil
			}
			return storage.RowVersion{}, false, SQLError("40001", "could not serialize access due to concurrent update")
		}
	}
	return current, present, nil
}

func (s *StatementScope) physicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode lock.MultiMode, successor ids.VersionID) error {
	e := s.Engine()
	e.Mu.Lock()
	err := e.AssignTransactionXIDLocked(s.TxRef)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	return s.Engine().Locks.PhysicalEvents(s.LockTicket, []lock.MultiEvent{{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: ref, Row: row.Row}, row.Version}, Mode: mode, Successor: successor}})
}

func (s *StatementScope) flushPhysicalEvents() error {
	events := s.multiEvents
	if len(events) == 0 {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	assignmentErr := e.AssignTransactionXIDLocked(s.TxRef)
	e.Mu.Unlock()
	if assignmentErr != nil {
		clear(events)
		s.multiEvents = events[:0]
		return assignmentErr
	}
	err := s.Engine().Locks.PhysicalEvents(s.LockTicket, events)
	clear(events)
	s.multiEvents = events[:0]
	return err
}
