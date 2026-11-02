package core

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// The source manifest identifies the grant. Manager waits never retain owner or
// Engine mutexes; repaired headers split only the exact target's resource.
func (h *statementHost) AcquireQueryRow(leaf exec.Leaf, row storage.RowVersion, mark plan.RowMark) (bool, error) {
	s := h.scope()
	if err := s.Active(); err != nil {
		return false, err
	}
	if s.LockTicket == nil || s.LockTicket.Ref() != s.TxRef || session.TxOwnerOf(s.TxRef) != s.Owner || s.TxRef.Session() != s.Op.Session.SessionToken || row.Row == 0 || row.Version == 0 || leaf.Storage == nil {
		return false, fmt.Errorf("stuntgres: invalid query row acquisition authority")
	}
	e := s.Engine()
	e.Mu.Lock()
	_, err := CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.PendingCatalog()), s.PendingCatalog(), leaf)
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, NoWait: mark.Wait != parser.RowLockBlock, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	err = s.acquireTuple(leaf.Ref, row, leaf.Storage, mark.Mode, policy)
	if errors.Is(err, lock.ErrUnavailable) {
		if mark.Wait == parser.RowLockSkip {
			return false, nil
		}
		return false, SQLError("55P03", fmt.Sprintf("could not obtain lock on row in relation %q", leaf.Relation.Name()))
	}
	if err != nil {
		return false, err
	}
	e.Mu.Lock()
	_, err = CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.PendingCatalog()), s.PendingCatalog(), leaf)
	if err == nil {
		err = e.AssignTransactionXIDLocked(s.TxRef)
	}
	e.Mu.Unlock()
	return err == nil, err
}

// Resolve exact physical membership and only genuine successor links. The
// stable KEY SHARE exception preserves its original snapshot value while the
// event still belongs to the actually admitted current physical tuple.
func (h *statementHost) LatestQueryRow(leaf exec.Leaf, candidate storage.RowVersion, hint int, mark plan.RowMark) (storage.RowVersion, bool, error) {
	s := h.scope()
	e := s.Engine()
	original := candidate
	for {
		e.Mu.Lock()
		_, err := CurrentQuerySourceLocked(e, e.PrivateCatalogLocked(s.PendingCatalog()), s.PendingCatalog(), leaf)
		var latest dml.LatestRow
		if err == nil {
			src := s.latestRowSourceLocked(e, leaf.Ref.Address)
			src.Stable = false
			latest, err = dml.LatestUpdateRow(src, dml.RowRequest{Ref: leaf.Ref, Row: candidate.Row, Physical: candidate.Version, Hint: hint})
		}
		current := latest.Version
		result := current
		var resource lock.Resource
		if err == nil {
			resource, err = s.tupleResourceLocked(leaf.Ref, current, leaf.Storage)
		}
		if err == nil && s.Owner.Isolation.Stable() && current.Born != 0 {
			changed := current.Born > s.Owner.Frontier
			if leaf.Storage.Ends != nil {
				end, ok := leaf.Storage.Ends.Lookup(current.Row, current.Version)
				changed = changed || ok && end.End > s.Owner.Frontier
			}
			if changed {
				if mark.Mode == lock.ForKeyShare && latest.Live && current.Version != original.Version && current.KeyChangedAt <= s.Owner.Frontier {
					result = original
				} else {
					err = SQLError("40001", "could not serialize access due to concurrent update")
				}
			}
		}
		admitted := s.admittedTuple(leaf.Ref, candidate)
		if err == nil && resource == admitted {
			s.rememberTuple(leaf.Ref, current, admitted)
		}
		e.Mu.Unlock()
		if err != nil {
			return storage.RowVersion{}, false, err
		}
		if !latest.Live {
			return result, false, nil
		}
		if resource != admitted {
			ok, err := h.AcquireQueryRow(leaf, current, mark)
			if err != nil || !ok {
				return storage.RowVersion{}, false, err
			}
			candidate = current
			continue
		}
		kind, err := lock.MultiLockMode(mark.Mode)
		if err != nil {
			return storage.RowVersion{}, false, err
		}
		if err := s.recordPhysicalRowLock(admitted, current, kind); err != nil {
			return storage.RowVersion{}, false, err
		}
		return result, true, nil
	}
}

// Immediate lock-only effects belong to the grant already acquired/rechecked by
// the caller. Repair can replace its header key afterwards, but must not move
// this old grant's event to a newer key or turn the publisher into another wait.
// Updating effects are published only by actual Stage acceptance with its real
// predecessor/successor, never by an unaccepted DML proposal.
func (s *StatementScope) physicalRowLock(ref ids.ObjectRef, row storage.RowVersion, mode lock.Mode) error {
	kind, err := lock.MultiLockMode(mode)
	if err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	admitted := s.admittedTuple(ref, row)
	err = e.AssignTransactionXIDLocked(s.TxRef)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	return s.recordPhysicalRowLock(admitted, row, kind)
}

func (s *StatementScope) recordPhysicalRowLock(resource lock.Resource, row storage.RowVersion, mode lock.MultiMode) error {
	e := s.Engine()
	return e.Locks.PhysicalEvents(s.LockTicket, []lock.MultiEvent{{Tuple: lock.MultiTuple{Resource: resource, Version: row.Version}, Mode: mode}})
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
	err := e.Locks.PhysicalEvents(s.LockTicket, events)
	clear(events)
	s.multiEvents = events[:0]
	return err
}
